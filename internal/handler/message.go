package handler

import (
	"context"
	"log/slog"
	"time"

	"cloud.google.com/go/pubsub/v2"
	"github.com/ctrlaltcloud008-hub/prj-apex-core-modules/pkg/apperror"
	"github.com/ctrlaltcloud008-hub/prj-apex-core-modules/pkg/logger"
	pbclient "github.com/ctrlaltcloud008-hub/prj-apex-core-modules/pkg/pubsub"
	"github.com/ctrlaltcloud008-hub/prj-apex-probe-service/internal/event"
	"github.com/ctrlaltcloud008-hub/prj-apex-probe-service/internal/service"
)

type Handler struct {
	logger    *logger.Logger
	processor *service.MessageProcessor
	region    string
}

func NewHandler(logger *logger.Logger, processor *service.MessageProcessor, region string) *Handler {
	return &Handler{
		logger:    logger,
		processor: processor,
		region:    region,
	}
}

// shouldAck returns true when the error is permanent (message-level) and Acking is the correct response.
// Infrastructure failures (transient or ambiguous) return false so Pub/Sub retries.
func shouldAck(err error) bool {
	switch apperror.Classify(err) {
	case apperror.Transient, apperror.Ambiguous:
		return false
	default:
		return true
	}
}

func (h *Handler) HandleMessage(ctx context.Context, msg *pubsub.Message) {
	ctx, span := pbclient.StartConsumerSpan(ctx, msg, "probe.receive")
	defer span.End()

	log := h.logger.WithSpanContext(ctx)
	requestID := msg.ID

	payload, err := event.ParseVideoReceived(msg)
	if err != nil {
		log.Error(ctx,
			"message.parse_error",
			"Failed to parse video.received message",
			slog.String("request_id", requestID),
			slog.String("error", err.Error()),
			slog.Bool("audit", true),
		)
		msg.Ack()
		return
	}

	// Enforce an absolute 5-minute wall-clock limit on the probe operation.
	// The subscriber library extends the ack deadline automatically while this goroutine runs.
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	err = h.processor.ProcessMessage(probeCtx, h.region, payload)
	if err != nil {
		log.Error(ctx,
			"message.processing_error",
			"Failed to process video.received message",
			slog.String("request_id", requestID),
			slog.String("video_id", payload.VideoID),
			slog.String("user_id", payload.UserID),
			slog.String("error", err.Error()),
			slog.Bool("audit", true),
		)
		if shouldAck(err) {
			if mfErr := h.processor.MarkFailed(ctx, payload, err); mfErr != nil {
				log.Error(ctx,
					"message.mark_failed_error",
					"Failed to mark video as FAILED in Spanner",
					slog.String("request_id", requestID),
					slog.String("video_id", payload.VideoID),
					slog.String("error", mfErr.Error()),
					slog.Bool("audit", true),
				)
			}
			msg.Ack()
		} else {
			msg.Nack()
		}
		return
	}

	log.Info(ctx,
		"message.processed",
		"Successfully processed video.received message",
		slog.String("request_id", requestID),
		slog.String("video_id", payload.VideoID),
		slog.String("user_id", payload.UserID),
		slog.Bool("audit", true),
	)
	msg.Ack()
}
