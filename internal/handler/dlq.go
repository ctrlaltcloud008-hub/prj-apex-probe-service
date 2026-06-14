package handler

import (
	"context"
	"errors"
	"log/slog"

	"cloud.google.com/go/pubsub/v2"
	"github.com/ctrlaltcloud008-hub/prj-apex-core-modules/pkg/apperror"
	pbclient "github.com/ctrlaltcloud008-hub/prj-apex-core-modules/pkg/pubsub"
	"github.com/ctrlaltcloud008-hub/prj-apex-probe-service/internal/event"
)

var errDeliveryExhausted = errors.New("video.received delivery attempts exhausted")

// HandleDLQMessage processes video.received messages that exhausted their
// delivery attempts. The video can never progress past VALIDATING on its own,
// so mark it FAILED with structured error details.
func (h *Handler) HandleDLQMessage(ctx context.Context, msg *pubsub.Message) {
	ctx, span := pbclient.StartConsumerSpan(ctx, msg, "probe.dlq.receive")
	defer span.End()

	log := h.logger.WithSpanContext(ctx)
	requestID := msg.ID

	payload, err := event.ParseVideoReceived(msg)
	if err != nil {
		log.Error(ctx,
			"dlq.parse_error",
			"Failed to parse dead-lettered video.received message; discarding",
			slog.String("request_id", requestID),
			slog.String("error", err.Error()),
			slog.Bool("audit", true),
		)
		msg.Ack()
		return
	}

	if err := h.processor.MarkFailed(ctx, payload, errDeliveryExhausted); err != nil {
		// Nack transient/ambiguous failures so the FAILED write is retried.
		if apperror.Classify(err) == apperror.Permanent {
			msg.Ack()
			return
		}
		log.Error(ctx,
			"dlq.mark_failed_error",
			"Failed to mark dead-lettered video as FAILED; will retry",
			slog.String("request_id", requestID),
			slog.String("video_id", payload.VideoID),
			slog.String("error", err.Error()),
			slog.Bool("audit", true),
		)
		msg.Nack()
		return
	}

	log.Info(ctx,
		"dlq.processed",
		"Dead-lettered video marked FAILED",
		slog.String("request_id", requestID),
		slog.String("video_id", payload.VideoID),
		slog.String("outcome", "success"),
		slog.Bool("audit", true),
	)
	msg.Ack()
}
