package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/ctrlaltcloud008-hub/prj-apex-core-modules/pkg/lifecycle"
	"github.com/ctrlaltcloud008-hub/prj-apex-core-modules/pkg/logger"
	"github.com/ctrlaltcloud008-hub/prj-apex-core-modules/pkg/models"
	"github.com/ctrlaltcloud008-hub/prj-apex-core-modules/pkg/outbox"
	spannerutils "github.com/ctrlaltcloud008-hub/prj-apex-core-modules/pkg/spanner"
	"github.com/ctrlaltcloud008-hub/prj-apex-probe-service/internal/event"
	"github.com/ctrlaltcloud008-hub/prj-apex-probe-service/internal/ffprobe"
	gcsclient "github.com/ctrlaltcloud008-hub/prj-apex-probe-service/internal/gcs"
	"github.com/ctrlaltcloud008-hub/prj-apex-probe-service/internal/probe"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const instrumentationName = "probe-service/internal/service"

// gcsHeaderBytes is the number of bytes read from GCS for ffprobe analysis.
// Well-formed MP4/MOV files have the moov atom in the first few MB; 30 MB covers
// the vast majority of real-world uploads without reading the full file.
const gcsHeaderBytes = 30 * 1024 * 1024

type errorDetails struct {
	Cause string `json:"cause"`
}

type MessageProcessor struct {
	logger  *logger.Logger
	spanner *spanner.Client
	gcs     *gcsclient.Client
}

func NewMessageProcessor(log *logger.Logger, spannerClient *spanner.Client, gcsClient *gcsclient.Client) *MessageProcessor {
	return &MessageProcessor{
		logger:  log,
		spanner: spannerClient,
		gcs:     gcsClient,
	}
}

func (p *MessageProcessor) ProcessMessage(ctx context.Context, region string, payload *event.VideoReceivedPayload) error {
	videoID := payload.VideoID
	userID := payload.UserID

	ctx, span := otel.Tracer(instrumentationName).Start(ctx,
		"probe.process_message",
		trace.WithAttributes(
			attribute.String("video_id", videoID),
			attribute.String("user_id", userID),
			attribute.String("bucket", payload.Bucket),
			attribute.String("object_path", payload.ObjectPath),
		),
	)
	defer span.End()

	log := p.logger.WithSpanContext(ctx)

	span.AddEvent("processing.idempotency_check")
	shouldContinue, err := p.checkShouldProcess(ctx, videoID, log)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(otelcodes.Error, "idempotency check failed")
		return fmt.Errorf("idempotency check: %w", err)
	}
	if !shouldContinue {
		span.SetStatus(otelcodes.Ok, "skipped")
		return nil
	}

	span.AddEvent("processing.ffprobe")
	probeResult, err := p.probeObject(ctx, payload.Bucket, payload.ObjectPath, log, videoID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(otelcodes.Error, "ffprobe failed")
		log.Error(ctx, "ffprobe.error", "ffprobe analysis failed",
			slog.String("video_id", videoID),
			slog.String("error", err.Error()),
			slog.String("outcome", "failure"),
			slog.Bool("audit", true),
		)
		return err
	}

	log.Info(ctx, "ffprobe.success", "ffprobe analysis complete",
		slog.Bool("has_audio", probeResult.HasAudio),
		slog.String("video_id", videoID),
		slog.Int("width", probeResult.Width),
		slog.Int("height", probeResult.Height),
		slog.String("codec", probeResult.Codec),
		slog.Int64("duration_ms", probeResult.DurationMs),
		slog.Float64("fps", probeResult.FPS),
		slog.Int64("bitrate_kbps", probeResult.BitrateKbps),
		slog.Bool("is_hdr", probeResult.IsHDR),
		slog.Bool("audit", false),
	)

	ladder := probe.BuildLadder(probeResult.Width, probeResult.Height, probeResult.IsHDR)
	if len(ladder) == 0 {
		return status.Errorf(codes.InvalidArgument,
			"empty rendition ladder for source %dx%d — source resolution may be too small to transcode",
			probeResult.Width, probeResult.Height)
	}

	ladderJSON, err := json.Marshal(ladder)
	if err != nil {
		return fmt.Errorf("marshal rendition ladder: %w", err)
	}
	ladderStr := string(ladderJSON)

	var ladderValue any
	if err := json.Unmarshal(ladderJSON, &ladderValue); err != nil {
		return fmt.Errorf("normalize rendition ladder for spanner JSON: %w", err)
	}

	sourceGCSURI := fmt.Sprintf("gs://%s/%s", payload.Bucket, payload.ObjectPath)
	transitionedAt := time.Now().UTC()

	span.AddEvent("processing.spanner_transaction")
	_, err = spannerutils.RunRW(ctx, p.spanner, func(ctx context.Context, tx *spanner.ReadWriteTransaction) error {
		if err := tx.BufferWrite([]*spanner.Mutation{
			spanner.Update("videos",
				[]string{
					"video_id", "status",
					"duration_ms", "source_width", "source_height",
					"source_codec", "source_fps", "is_hdr", "has_audio",
					"rendition_ladder", "updated_at",
				},
				[]any{
					videoID, string(models.StatusValidated),
					probeResult.DurationMs, int64(probeResult.Width), int64(probeResult.Height),
					probeResult.Codec, probeResult.FPS, probeResult.IsHDR, probeResult.HasAudio,
					spanner.NullJSON{Value: ladderValue, Valid: true}, spanner.CommitTimestamp,
				},
			),
		}); err != nil {
			return fmt.Errorf("buffer video update: %w", err)
		}

		if err := outbox.Write(ctx, tx, []outbox.Entry{{
			VideoID: videoID,
			Topic:   "video.validated",
			Payload: models.VideoValidatedPayload{
				VideoID:          videoID,
				UserID:           userID,
				SourceGCSURI:     sourceGCSURI,
				TranscodeProfile: ladderStr,
				SourceRegion:     region,
				GCSGeneration:    payload.GCSGeneration,
				Metadata: models.VideoValidatedMetadataPayload{
					DurationMs:  probeResult.DurationMs,
					Width:       probeResult.Width,
					Height:      probeResult.Height,
					Codec:       probeResult.Codec,
					FPS:         probeResult.FPS,
					IsHDR:       probeResult.IsHDR,
					BitrateKbps: probeResult.BitrateKbps,
				},
			},
		}}); err != nil {
			return fmt.Errorf("write outbox entry: %w", err)
		}

		if err := lifecycle.AppendLifecycleEvents(ctx, tx, videoID,
			lifecycle.LifeCycleEventParams{
				FromStatus: models.StatusValidating,
				ToStatus:   models.StatusValidated,
				Actor:      "probe",
				Reason:     "ffprobe metadata extraction complete",
			},
		); err != nil {
			return fmt.Errorf("append lifecycle events: %w", err)
		}

		if err := lifecycle.TransitionVideoStage(ctx, tx, lifecycle.StageTransitionParams{
			VideoID:        videoID,
			FromStage:      models.StatusValidating,
			FromAttempt:    1,
			ToStage:        models.StatusValidated,
			ToAttempt:      1,
			TransitionedAt: transitionedAt,
			Outcome:        "SUCCEEDED",
			Actor:          "probe",
		}); err != nil {
			return fmt.Errorf("transition validating stage: %w", err)
		}

		return nil
	})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(otelcodes.Error, "spanner transaction failed")
		log.Error(ctx, "spanner.transaction_error", "Spanner transaction failed",
			slog.String("video_id", videoID),
			slog.String("error", err.Error()),
			slog.String("outcome", "failure"),
			slog.Bool("audit", true),
		)
		return fmt.Errorf("spanner transaction: %w", err)
	}

	span.SetStatus(otelcodes.Ok, "video validated")
	log.Info(ctx, "message.processed", "Video validated successfully",
		slog.String("video_id", videoID),
		slog.String("user_id", userID),
		slog.String("outcome", "success"),
		slog.Bool("audit", true),
	)

	return nil
}

// probeObject runs ffprobe on the object's header range, falling back to a
// full-object stream when the failure looks like a non-faststart file (moov
// atom at the end). Only the fallback's verdict may classify the video as
// permanently bad — header-range "moov not found" alone must not FAIL a video.
func (p *MessageProcessor) probeObject(ctx context.Context, bucket, objectPath string, log *logger.Logger, videoID string) (*ffprobe.Result, error) {
	reader, err := p.gcs.ReadHeader(ctx, bucket, objectPath, gcsHeaderBytes)
	if err != nil {
		return nil, fmt.Errorf("read gcs header: %w", err)
	}

	result, probeErr := ffprobe.Probe(ctx, reader)
	reader.Close()
	if probeErr == nil {
		return result, nil
	}

	if !ffprobe.IsLikelyTruncatedInput(probeErr) {
		return nil, probeErr
	}

	log.Info(ctx, "ffprobe.fallback", "header probe failed with truncated-input signature; retrying with full object stream",
		slog.String("video_id", videoID),
		slog.String("header_error", probeErr.Error()),
		slog.Bool("audit", false),
	)

	fullReader, err := p.gcs.ReadFull(ctx, bucket, objectPath)
	if err != nil {
		return nil, fmt.Errorf("read gcs full object: %w", err)
	}
	defer fullReader.Close()

	return ffprobe.Probe(ctx, fullReader)
}

// MarkFailed writes status=FAILED to Spanner for a video that cannot be probed.
// Called on permanent ffprobe errors (corrupted file, no video stream, etc.).
// This is best-effort: the caller Acks regardless of whether MarkFailed succeeds.
func (p *MessageProcessor) MarkFailed(ctx context.Context, payload *event.VideoReceivedPayload, causeErr error) error {
	videoID := payload.VideoID
	transitionedAt := time.Now().UTC()

	ctx, span := otel.Tracer(instrumentationName).Start(ctx,
		"probe.mark_failed",
		trace.WithAttributes(attribute.String("video_id", videoID)),
	)
	defer span.End()

	log := p.logger.WithSpanContext(ctx)

	_, err := spannerutils.RunRW(ctx, p.spanner, func(ctx context.Context, tx *spanner.ReadWriteTransaction) error {
		if err := tx.BufferWrite([]*spanner.Mutation{
			spanner.Update("videos",
				[]string{"video_id", "status", "error_details", "updated_at"},
				[]any{
					videoID,
					string(models.StatusFailed),
					spanner.NullJSON{Value: errorDetails{Cause: causeErr.Error()}, Valid: true},
					spanner.CommitTimestamp,
				},
			),
		}); err != nil {
			return fmt.Errorf("buffer videos update: %w", err)
		}

		if err := lifecycle.AppendLifecycleEvents(ctx, tx, videoID,
			lifecycle.LifeCycleEventParams{
				FromStatus: models.StatusValidating,
				ToStatus:   models.StatusFailed,
				Actor:      "probe",
				Reason:     causeErr.Error(),
			},
		); err != nil {
			return fmt.Errorf("append lifecycle events: %w", err)
		}

		if err := lifecycle.TransitionVideoStage(ctx, tx, lifecycle.StageTransitionParams{
			VideoID:        videoID,
			FromStage:      models.StatusValidating,
			FromAttempt:    1,
			ToStage:        models.StatusFailed,
			ToAttempt:      1,
			TransitionedAt: transitionedAt,
			Outcome:        "FAILED",
			Actor:          "probe",
		}); err != nil {
			return fmt.Errorf("transition failed stage: %w", err)
		}

		return nil
	})
	if err != nil {
		span.RecordError(err)
		log.Error(ctx, "spanner.mark_failed_error", "Failed to write FAILED status to Spanner",
			slog.String("video_id", videoID),
			slog.String("error", err.Error()),
			slog.Bool("audit", true),
		)
		return fmt.Errorf("mark failed transaction: %w", err)
	}

	span.SetStatus(otelcodes.Ok, "video marked FAILED")
	log.Info(ctx, "video.marked_failed", "Video marked as FAILED",
		slog.String("video_id", videoID),
		slog.String("cause", causeErr.Error()),
		slog.Bool("audit", true),
	)

	return nil
}

// checkShouldProcess reads the current video status from Spanner.
// Returns false (skip + Ack) if the status is not VALIDATING or the video doesn't exist.
// Returns an error only on infrastructure failures (transient → Nack).
func (p *MessageProcessor) checkShouldProcess(ctx context.Context, videoID string, log *logger.Logger) (bool, error) {
	var currentStatus string

	err := spannerutils.RunRO(ctx, p.spanner, func(ctx context.Context, tx *spanner.ReadOnlyTransaction) error {
		row, err := tx.ReadRow(ctx, "videos", spanner.Key{videoID}, []string{"status"})
		if err != nil {
			return err
		}
		return row.ColumnByName("status", &currentStatus)
	})
	if err != nil {
		if spanner.ErrCode(err) == codes.NotFound {
			log.Error(ctx, "spanner.video_not_found", "Video not found in Spanner; discarding message",
				slog.String("video_id", videoID),
				slog.Bool("audit", true),
			)
			return false, nil
		}
		return false, fmt.Errorf("read video status: %w", err)
	}

	if currentStatus != string(models.StatusValidating) {
		log.Info(ctx, "processing.skipped", "Video is not in VALIDATING status; skipping",
			slog.String("video_id", videoID),
			slog.String("current_status", currentStatus),
			slog.Bool("audit", true),
		)
		return false, nil
	}

	return true, nil
}
