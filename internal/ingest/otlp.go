package ingest

import (
	"context"

	"traceboard/internal/event"
	"traceboard/internal/otlp"
)

// OTLPService decodes OpenTelemetry signals and feeds them through the exact
// same prepare, redact, and store path used by the JSON endpoint. No source can
// bypass capture-mode filtering or pre-persistence redaction.
type OTLPService struct {
	service *Service
	limits  Limits
}

func NewOTLPService(service *Service) *OTLPService {
	return &OTLPService{service: service, limits: service.limits}
}

func (otlpService *OTLPService) IngestOTLP(ctx context.Context, source string, body []byte, contentType string) Result {
	return otlpService.IngestSignal(ctx, source, body, contentType, otlpKindForSource(source))
}

func otlpKindForSource(source string) otlp.Kind {
	switch source {
	case "otlp-logs":
		return otlp.KindLogs
	case "otlp-metrics":
		return otlp.KindMetrics
	default:
		return otlp.KindTraces
	}
}

func (otlpService *OTLPService) IngestSignal(ctx context.Context, source string, body []byte, contentType string, kind otlp.Kind) Result {
	signal, err := otlp.Decode(kind, contentType, body, int64(otlpService.limits.MaxRawPayloadSize)*8)
	if err != nil {
		return Result{Quarantined: 1}
	}
	context := otlp.SourceContext{
		Source:      ResolveSourceName(signal, source),
		CaptureMode: otlpService.service.CaptureMode(ResolveSourceName(signal, source)),
	}
	events := otlp.Normalize(signal, context)
	for index := range events {
		events[index].Source = context.Source
	}
	if len(events) == 0 {
		return Result{}
	}
	return otlpService.service.Ingest(ctx, event.Batch{Events: events})
}

// CombinedService is the single write path the HTTP server depends on. It
// serves both the JSON envelope endpoint and the three OTLP endpoints so the
// server has one dependency instead of two.
type CombinedService struct {
	events *Service
	otlp   *OTLPService
}

func NewCombinedService(events *Service, otlpService *OTLPService) *CombinedService {
	return &CombinedService{events: events, otlp: otlpService}
}

func (combined *CombinedService) Ingest(ctx context.Context, batch event.Batch) Result {
	return combined.events.Ingest(ctx, batch)
}

func (combined *CombinedService) IngestOTLP(ctx context.Context, source string, body []byte, contentType string) Result {
	return combined.otlp.IngestOTLP(ctx, source, body, contentType)
}

// ResolveSourceName prefers the exporter's own service.name so an agent
// identifies itself, and falls back to the configured endpoint source.
func ResolveSourceName(signal otlp.Signal, fallback string) string {
	names := otlp.OtlpSourceNames(signal)
	for _, name := range names {
		if name != "" {
			return name
		}
	}
	return fallback
}
