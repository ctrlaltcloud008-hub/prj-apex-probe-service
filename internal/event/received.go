package event

import (
	"encoding/json"
	"fmt"

	"cloud.google.com/go/pubsub/v2"
)

type VideoReceivedPayload struct {
	VideoID       string `json:"video_id"`
	UserID        string `json:"user_id"`
	Bucket        string `json:"bucket"`
	ObjectPath    string `json:"object_path"`
	GCSGeneration int64  `json:"gcs_generation"`
	SourceRegion  string `json:"source_region"`
}

func ParseVideoReceived(msg *pubsub.Message) (*VideoReceivedPayload, error) {
	if msg == nil {
		return nil, fmt.Errorf("nil message")
	}

	// The outbox relay publishes the unwrapped payload as the message body and
	// carries trace context (traceparent/tracestate) in Pub/Sub attributes, so
	// the body is the VideoReceivedPayload directly — not a full outbox envelope.
	var payload VideoReceivedPayload
	if err := json.Unmarshal(msg.Data, &payload); err != nil {
		return nil, fmt.Errorf("unmarshal video received payload: %w", err)
	}

	if payload.VideoID == "" {
		return nil, fmt.Errorf("missing video_id in payload")
	}
	if payload.Bucket == "" {
		return nil, fmt.Errorf("missing bucket in payload")
	}
	if payload.ObjectPath == "" {
		return nil, fmt.Errorf("missing object_path in payload")
	}

	return &payload, nil
}
