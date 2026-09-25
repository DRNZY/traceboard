package otlp

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"time"

	collectorlogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	collectormetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	collectortracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"

	"traceboard/internal/event"
)

// SourceContext describes where a signal arrived. Agent sources identify
// themselves through service.name; the caller supplies the fallback.
type SourceContext struct {
	Source         string
	SourceVersion  string
	CaptureMode    event.CaptureMode
	ProjectID      string
	ParentRunID    string
	ConversationID string
}

// Normalize converts a decoded signal into Traceboard events. It never invents
// a run identity: a record without a usable conversation or trace identifier is
// stored as a source extension with the gap recorded.
func Normalize(signal Signal, context SourceContext) []event.Event {
	switch signal.Kind {
	case KindTraces:
		return normalizeTraces(signal.Traces, context)
	case KindLogs:
		return normalizeLogs(signal.Logs, context)
	case KindMetrics:
		return normalizeMetrics(signal.Metrics, context)
	default:
		return nil
	}
}

func normalizeTraces(request *collectortracepb.ExportTraceServiceRequest, context SourceContext) []event.Event {
	if request == nil {
		return nil
	}
	events := make([]event.Event, 0, 16)
	for _, resourceSpans := range request.ResourceSpans {
		resource := resourceAttributes(resourceSpans.GetResource())
		source := sourceName(resource, context.Source)
		sourceVersion := sourceVersion(resource, context.SourceVersion)
		for _, scopeSpans := range resourceSpans.GetScopeSpans() {
			scope := ""
			if scopeSpans.GetScope() != nil {
				scope = scopeSpans.GetScope().GetName()
			}
			for _, span := range scopeSpans.GetSpans() {
				spanContext := context
				spanContext.Source = source
				events = append(events, traceSpanEvents(source, sourceVersion, scope, resource, span, spanContext)...)
			}
		}
	}
	sort.SliceStable(events, func(left, right int) bool {
		return events[left].OccurredAt.Before(events[right].OccurredAt)
	})
	return events
}

func traceSpanEvents(source, sourceVersion, scope string, resource map[string]any, span *tracepb.Span, context SourceContext) []event.Event {
	attributes := attributesToMap(span.GetAttributes())
	runID := runIdentifier(span, attributes, context)
	stepID := spanID(span)
	isRoot := span.GetParentSpanId() == nil || len(span.GetParentSpanId()) == 0
	occurredAt := spanTime(span)

	base := func(eventType string, status event.Status) event.Event {
		return event.Event{
			SchemaVersion:  event.SchemaVersion1,
			Source:         source,
			SourceVersion:  sourceVersion,
			RunID:          runID,
			StepID:         stepID,
			OccurredAt:     occurredAt,
			Type:           eventType,
			Status:         status,
			SourceSequence: nil,
		}
	}

	var events []event.Event

	if isRoot {
		started := base(event.TypeRunStarted, event.StatusStarted)
		started.StepID = ""
		started.Attributes = spanAttributes(source, scope, resource, attributes, context, span)
		events = append(events, started)
	}

	if operation, ok := attributes["gen_ai.operation.name"].(string); ok {
		switch operation {
		case "chat", "generate_content", "text_completion":
			requested := base(event.TypeModelRequested, event.StatusStarted)
			requested.StepID = stepID + ":request"
			requested.Attributes = modelAttributes(attributes, resource, context, span)
			events = append(events, requested)
		}
	}

	if operation, ok := attributes["gen_ai.operation.name"].(string); ok && isModelOperation(operation) {
		completed := base(event.TypeModelCompleted, spanStatus(span))
		completed.StepID = stepID + ":request"
		completed.Attributes = modelAttributes(attributes, resource, context, span)
		events = append(events, completed)
	}

	terminalType, terminalStatus := terminalEventFor(span, attributes)
	if terminalType == "" && isRootSpan(span) && span.GetEndTimeUnixNano() > 0 {
		// A finished root span is the end of the run unless it reported an error.
		terminalType, terminalStatus = event.TypeRunCompleted, event.StatusCompleted
	}
	if terminalType != "" {
		terminal := base(terminalType, terminalStatus)
		terminal.Attributes = spanAttributes(source, scope, resource, attributes, context, span)
		if terminalType == event.TypeRunCompleted {
			terminal.StepID = ""
		}
		if terminalStatus == event.StatusFailed && terminalType == event.TypeRunFailed {
			terminal.Content = map[string]any{"text": errorText(span, attributes)}
		}
		events = append(events, terminal)
	}

	if len(events) == 0 {
		extension := base(event.TypeSourceExtension, event.StatusUnknown)
		extension.Attributes = spanAttributes(source, scope, resource, attributes, context, span)
		extension.Attributes["extension_kind"] = "span"
		extension.Attributes["source_type"] = span.GetName()
		events = append(events, extension)
	}
	return events
}

func normalizeLogs(request *collectorlogspb.ExportLogsServiceRequest, context SourceContext) []event.Event {
	if request == nil {
		return nil
	}
	events := make([]event.Event, 0, 16)
	for _, resourceLogs := range request.ResourceLogs {
		resource := resourceAttributes(resourceLogs.GetResource())
		source := sourceName(resource, context.Source)
		sourceVersion := sourceVersion(resource, context.SourceVersion)
		for _, scopeLogs := range resourceLogs.GetScopeLogs() {
			scope := ""
			if scopeLogs.GetScope() != nil {
				scope = scopeLogs.GetScope().GetName()
			}
			for _, record := range scopeLogs.GetLogRecords() {
				attributes := attributesToMap(record.GetAttributes())
				occurredAt := recordTimeNano(record)
				eventType := logEventType(record, attributes)
				output := event.Event{
					SchemaVersion:  event.SchemaVersion1,
					Source:         source,
					SourceVersion:  sourceVersion,
					RunID:          runIdentifierFromAttributes(attributes, context),
					OccurredAt:     occurredAt,
					Type:           eventType,
					Status:         logStatus(record),
					SourceSequence: nil,
				}
				if traceID := record.GetTraceId(); len(traceID) > 0 {
					output.StepID = "span:" + hex.EncodeToString(traceID)
				}
				output.Attributes = map[string]any{
					"source_service":  source,
					"scope":           scope,
					"severity":        severityName(record.GetSeverityNumber()),
					"source_resource": resource,
					"extension_kind":  "log",
				}
				for key, value := range attributes {
					output.Attributes["source."+key] = value
				}
				if body := record.GetBody().GetStringValue(); body != "" && context.CaptureMode == event.CaptureDetailed {
					output.Content = map[string]any{"text": body}
				}
				events = append(events, output)
			}
		}
	}
	sort.SliceStable(events, func(left, right int) bool {
		return events[left].OccurredAt.Before(events[right].OccurredAt)
	})
	return events
}

func normalizeMetrics(request *collectormetricspb.ExportMetricsServiceRequest, context SourceContext) []event.Event {
	if request == nil {
		return nil
	}
	events := make([]event.Event, 0, 16)
	for _, resourceMetrics := range request.ResourceMetrics {
		resource := resourceAttributes(resourceMetrics.GetResource())
		source := sourceName(resource, context.Source)
		sourceVersion := sourceVersion(resource, context.SourceVersion)
		for _, scopeMetrics := range resourceMetrics.GetScopeMetrics() {
			scope := ""
			if scopeMetrics.GetScope() != nil {
				scope = scopeMetrics.GetScope().GetName()
			}
			for _, metric := range scopeMetrics.GetMetrics() {
				events = append(events, event.Event{
					SchemaVersion: event.SchemaVersion1,
					Source:        source,
					SourceVersion: sourceVersion,
					RunID:         context.Source + ":metrics",
					OccurredAt:    time.Now().UTC(),
					Type:          event.TypeSourceExtension,
					Status:        event.StatusUnknown,
					Attributes: map[string]any{
						"extension_kind":     "metric",
						"scope":              scope,
						"metric_name":        metric.GetName(),
						"metric_unit":        metric.GetUnit(),
						"metric_description": metric.GetDescription(),
					},
				})
			}
		}
	}
	return events
}

func sourceName(resource map[string]any, fallback string) string {
	if name, ok := resource["service.name"].(string); ok && name != "" {
		return name
	}
	if fallback != "" {
		return fallback
	}
	return "unknown-source"
}

func sourceVersion(resource map[string]any, fallback string) string {
	for _, key := range []string{"service.version", "telemetry.sdk.version", "process.executable.version"} {
		if version, ok := resource[key].(string); ok && version != "" {
			return version
		}
	}
	return fallback
}

func runIdentifier(span *tracepb.Span, attributes map[string]any, context SourceContext) string {
	for _, key := range []string{"gen_ai.conversation.id", "session.id", "conversation.id", "langfuse.trace.id"} {
		if value, ok := attributes[key].(string); ok && value != "" {
			return context.Source + ":" + value
		}
	}
	if conversation := context.ConversationID; conversation != "" {
		return context.Source + ":" + conversation
	}
	if traceID := span.GetTraceId(); len(traceID) > 0 {
		return context.Source + ":trace:" + hex.EncodeToString(traceID)
	}
	return context.Source + ":span:" + spanID(span)
}

func runIdentifierFromAttributes(attributes map[string]any, context SourceContext) string {
	for _, key := range []string{"gen_ai.conversation.id", "session.id", "conversation.id"} {
		if value, ok := attributes[key].(string); ok && value != "" {
			return context.Source + ":" + value
		}
	}
	if context.ConversationID != "" {
		return context.Source + ":" + context.ConversationID
	}
	return context.Source + ":unattributed"
}

func spanID(span *tracepb.Span) string {
	if id := span.GetSpanId(); len(id) > 0 {
		return "span:" + hex.EncodeToString(id)
	}
	sum := sha256.Sum256([]byte(span.GetName() + hex.EncodeToString(span.GetTraceId()) + spanTime(span).String()))
	return "span:" + hex.EncodeToString(sum[:8])
}

func spanTime(span *tracepb.Span) time.Time {
	if start := span.GetStartTimeUnixNano(); start > 0 {
		return time.Unix(0, int64(start)).UTC()
	}
	if end := span.GetEndTimeUnixNano(); end > 0 {
		return time.Unix(0, int64(end)).UTC()
	}
	return time.Now().UTC()
}

func recordTimeNano(record *logspb.LogRecord) time.Time {
	if value := record.GetTimeUnixNano(); value > 0 {
		return time.Unix(0, int64(value)).UTC()
	}
	if value := record.GetObservedTimeUnixNano(); value > 0 {
		return time.Unix(0, int64(value)).UTC()
	}
	return time.Now().UTC()
}

func spanStatus(span *tracepb.Span) event.Status {
	switch span.GetStatus().GetCode() {
	case tracepb.Status_STATUS_CODE_ERROR:
		return event.StatusFailed
	case tracepb.Status_STATUS_CODE_OK:
		return event.StatusCompleted
	default:
		return event.StatusUnknown
	}
}

func terminalEventFor(span *tracepb.Span, attributes map[string]any) (string, event.Status) {
	if value, ok := attributes["error.type"].(string); ok && value != "" {
		if isRootSpan(span) {
			return event.TypeRunFailed, event.StatusFailed
		}
		return event.TypeToolFailed, event.StatusFailed
	}
	if span.GetStatus().GetCode() == tracepb.Status_STATUS_CODE_ERROR {
		if isRootSpan(span) {
			return event.TypeRunFailed, event.StatusFailed
		}
		return event.TypeErrorRecorded, event.StatusFailed
	}
	return "", ""
}

func isRootSpan(span *tracepb.Span) bool {
	return len(span.GetParentSpanId()) == 0
}

func isModelOperation(operation string) bool {
	switch operation {
	case "chat", "generate_content", "text_completion", "embeddings":
		return true
	default:
		return false
	}
}

func modelAttributes(attributes, resource map[string]any, context SourceContext, span *tracepb.Span) map[string]any {
	result := map[string]any{}
	for _, key := range []string{
		"gen_ai.request.model", "gen_ai.response.model", "gen_ai.usage.input_tokens",
		"gen_ai.usage.output_tokens", "gen_ai.usage.total_tokens", "llm.token.count",
	} {
		if value, ok := attributes[key]; ok {
			result[key] = value
		}
	}
	if model, ok := attributes["gen_ai.request.model"].(string); ok {
		result["model"] = model
	}
	if input, ok := attributes["gen_ai.usage.input_tokens"]; ok {
		result["input_tokens"] = input
	}
	if output, ok := attributes["gen_ai.usage.output_tokens"]; ok {
		result["output_tokens"] = output
	}
	if conversation, ok := attributes["gen_ai.conversation.id"].(string); ok {
		result["conversation_id"] = conversation
	}
	if context.ProjectID != "" {
		result["project_id"] = context.ProjectID
	}
	if len(resource) > 0 {
		result["source_resource"] = resource
	}
	return result
}

func spanAttributes(source, scope string, resource, attributes map[string]any, context SourceContext, span *tracepb.Span) map[string]any {
	result := map[string]any{
		"span_name":       span.GetName(),
		"source_service":  source,
		"scope":           scope,
		"span_kind":       span.GetKind().String(),
		"source_resource": resource,
	}
	for key, value := range attributes {
		result["source."+key] = value
	}
	if context.ProjectID != "" {
		result["project_id"] = context.ProjectID
	}
	if context.ParentRunID != "" {
		result["parent_run_id"] = context.ParentRunID
	}
	if parent := span.GetParentSpanId(); len(parent) > 0 {
		result["parent_step_id"] = "span:" + hex.EncodeToString(parent)
	}
	if traceID := span.GetTraceId(); len(traceID) > 0 {
		result["trace_id"] = hex.EncodeToString(traceID)
	}
	if status := span.GetStatus().GetMessage(); status != "" {
		result["status_message"] = status
	}
	return result
}

func logEventType(record *logspb.LogRecord, attributes map[string]any) string {
	if name, ok := attributes["event.name"].(string); ok {
		switch name {
		case "ai.user.message", "user_prompt", "prompt":
			return event.TypePromptReceived
		case "exception", "error":
			return event.TypeErrorRecorded
		case "tool.call", "function_call":
			return event.TypeToolStarted
		}
	}
	if record.GetSeverityNumber() >= logspb.SeverityNumber_SEVERITY_NUMBER_ERROR {
		return event.TypeErrorRecorded
	}
	return event.TypeSourceExtension
}

func logStatus(record *logspb.LogRecord) event.Status {
	if record.GetSeverityNumber() >= logspb.SeverityNumber_SEVERITY_NUMBER_ERROR {
		return event.StatusFailed
	}
	return event.StatusUnknown
}

func errorText(span *tracepb.Span, attributes map[string]any) string {
	if value, ok := attributes["error.message"].(string); ok && value != "" {
		return value
	}
	if message := span.GetStatus().GetMessage(); message != "" {
		return message
	}
	return fmt.Sprintf("span %s reported an error", span.GetName())
}

// OtlpSourceNames reports every distinct service.name carried by the signal, in
// first-seen order. The caller uses the first non-empty value as the source.
func OtlpSourceNames(signal Signal) []string {
	names := make([]string, 0, 2)
	seen := make(map[string]struct{}, 2)
	add := func(resource map[string]any) {
		name, ok := resource["service.name"].(string)
		if !ok || name == "" {
			return
		}
		if _, duplicate := seen[name]; duplicate {
			return
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	if signal.Traces != nil {
		for _, resourceSpans := range signal.Traces.GetResourceSpans() {
			add(resourceAttributes(resourceSpans.GetResource()))
		}
	}
	if signal.Logs != nil {
		for _, resourceLogs := range signal.Logs.GetResourceLogs() {
			add(resourceAttributes(resourceLogs.GetResource()))
		}
	}
	if signal.Metrics != nil {
		for _, resourceMetrics := range signal.Metrics.GetResourceMetrics() {
			add(resourceAttributes(resourceMetrics.GetResource()))
		}
	}
	return names
}
