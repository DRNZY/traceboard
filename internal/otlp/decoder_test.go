package otlp

import (
	"bytes"
	"compress/gzip"
	"strings"
	"testing"

	collectorlogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	collectormetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	collectortracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"

	"traceboard/internal/event"
)

const traceJSON = `{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"claude-code"}}]},"scopeSpans":[{"spans":[{"traceId":"5b8efff798038103d269b633813fc60c","spanId":"eee19b7ec3c1b174","name":"chat claude-sonnet","kind":3,"startTimeUnixNano":"1750000000000000000","endTimeUnixNano":"1750000001000000000","status":{"code":1},"attributes":[{"key":"gen_ai.operation.name","value":{"stringValue":"chat"}},{"key":"gen_ai.conversation.id","value":{"stringValue":"conv_1"}},{"key":"gen_ai.request.model","value":{"stringValue":"claude-sonnet-4"}},{"key":"gen_ai.usage.input_tokens","value":{"intValue":"1200"}}]}]}]}]}`

func TestDecodeJSONTraces(t *testing.T) {
	signal, err := Decode(KindTraces, "application/json", []byte(traceJSON), 1<<20)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if signal.Encoding != "json" {
		t.Fatalf("encoding = %q", signal.Encoding)
	}
	if len(signal.Traces.GetResourceSpans()) != 1 {
		t.Fatalf("resource spans = %d", len(signal.Traces.GetResourceSpans()))
	}
}

func TestDecodeProtobufTraces(t *testing.T) {
	encoded, err := proto.Marshal(sampleTraceRequest())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	signal, err := Decode(KindTraces, "application/x-protobuf", encoded, 1<<20)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if signal.Encoding != "protobuf" {
		t.Fatalf("encoding = %q", signal.Encoding)
	}
	if len(signal.Traces.GetResourceSpans()) != 1 {
		t.Fatalf("resource spans = %d", len(signal.Traces.GetResourceSpans()))
	}
}

func TestDecodeGzipBody(t *testing.T) {
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write([]byte(traceJSON)); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	signal, err := Decode(KindTraces, "application/json; charset=utf-8", compressed.Bytes(), 1<<20)
	if err != nil {
		t.Fatalf("decode gzip without the content-encoding hint: %v", err)
	}
	if len(signal.Traces.GetResourceSpans()) != 1 {
		t.Fatal("gzip body was not decoded")
	}
}

func TestDecodeRejectsOversizedDecompressedBody(t *testing.T) {
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write(bytes.Repeat([]byte("a"), 1<<20)); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	if _, err := Decode(KindTraces, "application/json", compressed.Bytes(), 1024); err != ErrPayloadTooLarge {
		t.Fatalf("oversized body = %v, want ErrPayloadTooLarge", err)
	}
}

func TestDecodeRejectsMalformedAndUnsupportedInput(t *testing.T) {
	if _, err := Decode(KindTraces, "application/json", []byte(`{"resourceSpans":`), 1<<20); err == nil {
		t.Fatal("expected malformed JSON to be rejected")
	}
	if _, err := Decode("signals", "application/json", []byte("{}"), 1<<20); err != ErrUnsupportedKind {
		t.Fatalf("unsupported kind = %v", err)
	}
	if _, err := Decode(KindTraces, "application/x-protobuf", []byte{0xff, 0xff, 0xff}, 1<<20); err == nil {
		t.Fatal("expected malformed protobuf to be rejected")
	}
}

func TestDecodeRejectsMalformedGzip(t *testing.T) {
	if _, err := Decode(KindTraces, "application/x-protobuf", []byte("not gzip at all"), 1<<20); err == nil {
		t.Fatal("expected a malformed gzip body to be rejected")
	}
}

func TestDecodeLogsAndMetrics(t *testing.T) {
	logsJSON := `{"resourceLogs":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"gemini-cli"}}]},"scopeLogs":[{"logRecords":[{"timeUnixNano":"1750000000000000000","body":{"stringValue":"turn started"},"severityNumber":9}]}]}]}`
	signal, err := Decode(KindLogs, "application/json", []byte(logsJSON), 1<<20)
	if err != nil {
		t.Fatalf("decode logs: %v", err)
	}
	if len(signal.Logs.GetResourceLogs()) != 1 {
		t.Fatal("logs were not decoded")
	}

	metricsJSON := `{"resourceMetrics":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"gemini-cli"}}]},"scopeMetrics":[{"metrics":[{"name":"token.usage","unit":"1"}]}]}]}`
	metrics, err := Decode(KindMetrics, "application/json", []byte(metricsJSON), 1<<20)
	if err != nil {
		t.Fatalf("decode metrics: %v", err)
	}
	if len(metrics.Metrics.GetResourceMetrics()) != 1 {
		t.Fatal("metrics were not decoded")
	}
}

func TestDecodeSniffsEncodingOverAJsonContentType(t *testing.T) {
	// A mislabelled body must still decode from its actual shape.
	if _, err := Decode(KindTraces, "application/x-protobuf", []byte(traceJSON), 1<<20); err != nil {
		t.Fatalf("decode: %v", err)
	}
	encoded, _ := proto.Marshal(sampleTraceRequest())
	if _, err := Decode(KindTraces, "application/json", encoded, 1<<20); err != nil {
		t.Fatalf("decode protobuf labelled as json: %v", err)
	}
}

func TestEmptySuccessEncodesPerSignal(t *testing.T) {
	// An empty protobuf message legitimately encodes to zero bytes; the
	// exporter only requires a decodable response.
	for _, kind := range []Kind{KindTraces, KindLogs, KindMetrics} {
		if EmptySuccess(kind) == nil {
			t.Fatalf("%s returned a nil response", kind)
		}
	}
	if EmptySuccess("other") != nil {
		t.Fatal("an unknown signal must not produce a response body")
	}
}

func TestNormalizeMapsRootSpanToRunLifecycle(t *testing.T) {
	signal, err := Decode(KindTraces, "application/json", []byte(traceJSON), 1<<20)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	events := Normalize(signal, SourceContext{CaptureMode: event.CaptureMetadata})
	if len(events) < 3 {
		t.Fatalf("normalized %d events: %+v", len(events), events)
	}
	for index := 1; index < len(events); index++ {
		if events[index].OccurredAt.Before(events[index-1].OccurredAt) {
			t.Fatalf("events are not ordered: %+v", events)
		}
	}
	if events[0].Type != event.TypeRunStarted {
		t.Fatalf("first event = %s, want run.started", events[0].Type)
	}
	if events[len(events)-1].Type != event.TypeRunCompleted {
		t.Fatalf("last event = %s, want run.completed", events[len(events)-1].Type)
	}
	for _, output := range events {
		if output.RunID != "claude-code:conv_1" {
			t.Fatalf("run id = %q, want the GenAI conversation id", output.RunID)
		}
		if output.Source != "claude-code" {
			t.Fatalf("source = %q, want the exporter service.name", output.Source)
		}
	}
	model := events[len(events)-2]
	if model.Attributes["model"] != "claude-sonnet-4" {
		t.Fatalf("model = %+v", model.Attributes["model"])
	}
	if model.Attributes["input_tokens"] != int64(1200) {
		t.Fatalf("input tokens = %+v", model.Attributes["input_tokens"])
	}
}

func TestNormalizeMapsSpanErrorsToRunFailure(t *testing.T) {
	signal, err := Decode(KindTraces, "application/json", []byte(traceJSON), 1<<20)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	span := signal.Traces.GetResourceSpans()[0].GetScopeSpans()[0].GetSpans()[0]
	span.Attributes = append(span.Attributes, &commonpb.KeyValue{
		Key:   "error.type",
		Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "Overloaded"}},
	}, &commonpb.KeyValue{
		Key:   "error.message",
		Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "the model is overloaded"}},
	})
	events := Normalize(signal, SourceContext{CaptureMode: event.CaptureMetadata})
	var sawFailure bool
	for _, output := range events {
		if output.Type == event.TypeRunFailed {
			sawFailure = true
			if output.Content["text"] != "the model is overloaded" {
				t.Fatalf("failure content = %+v", output.Content)
			}
		}
	}
	if !sawFailure {
		t.Fatalf("a reported error did not become a run failure: %+v", events)
	}
}

func TestNormalizeFallsBackToTraceIdentityWithoutConversation(t *testing.T) {
	signal, err := Decode(KindTraces, "application/json", []byte(traceJSON), 1<<20)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	span := signal.Traces.GetResourceSpans()[0].GetScopeSpans()[0].GetSpans()[0]
	span.Attributes = nil
	events := Normalize(signal, SourceContext{CaptureMode: event.CaptureMetadata})
	if len(events) == 0 {
		t.Fatal("no events were produced")
	}
	if !strings.HasPrefix(events[0].RunID, "claude-code:trace:") {
		t.Fatalf("run id = %q, want a trace-derived identity", events[0].RunID)
	}
}

func TestNormalizeLogsHonourCaptureMode(t *testing.T) {
	logsJSON := `{"resourceLogs":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"gemini-cli"}}]},"scopeLogs":[{"logRecords":[{"timeUnixNano":"1750000000000000000","body":{"stringValue":"a private tool result"},"severityNumber":9,"attributes":[{"key":"event.name","value":{"stringValue":"tool.call"}}]}]}]}]}`

	metadata, err := Decode(KindLogs, "application/json", []byte(logsJSON), 1<<20)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	events := Normalize(metadata, SourceContext{CaptureMode: event.CaptureMetadata})
	if len(events) != 1 {
		t.Fatalf("events = %d", len(events))
	}
	if events[0].Type != event.TypeToolStarted {
		t.Fatalf("type = %s", events[0].Type)
	}
	if events[0].Content != nil {
		t.Fatal("metadata mode captured a log body")
	}

	detailed, err := Decode(KindLogs, "application/json", []byte(logsJSON), 1<<20)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	events = Normalize(detailed, SourceContext{CaptureMode: event.CaptureDetailed})
	if events[0].Content["text"] != "a private tool result" {
		t.Fatalf("content = %+v", events[0].Content)
	}
}

func TestNormalizeMetricsBecomeExtensions(t *testing.T) {
	metricsJSON := `{"resourceMetrics":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"gemini-cli"}}]},"scopeMetrics":[{"metrics":[{"name":"token.usage","unit":"1","description":"tokens"}]}]}]}`
	signal, err := Decode(KindMetrics, "application/json", []byte(metricsJSON), 1<<20)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	events := Normalize(signal, SourceContext{CaptureMode: event.CaptureMetadata})
	if len(events) != 1 {
		t.Fatalf("events = %d", len(events))
	}
	if events[0].Type != event.TypeSourceExtension {
		t.Fatalf("type = %s, want a source extension", events[0].Type)
	}
	if events[0].Attributes["extension_kind"] != "metric" {
		t.Fatalf("extension_kind = %+v", events[0].Attributes["extension_kind"])
	}
	if events[0].Attributes["metric_name"] != "token.usage" {
		t.Fatalf("metric_name = %+v", events[0].Attributes["metric_name"])
	}
}

func TestNormalizeNilSignalsAreSafe(t *testing.T) {
	if events := Normalize(Signal{Kind: KindTraces}, SourceContext{}); events != nil {
		t.Fatalf("nil trace signal produced %+v", events)
	}
	if events := Normalize(Signal{Kind: "other"}, SourceContext{}); events != nil {
		t.Fatalf("unknown signal produced %+v", events)
	}
}

func sampleTraceRequest() *collectortracepb.ExportTraceServiceRequest {
	return &collectortracepb.ExportTraceServiceRequest{
		ResourceSpans: []*tracepb.ResourceSpans{{
			Resource: &resourcepb.Resource{Attributes: []*commonpb.KeyValue{
				{Key: "service.name", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "claude-code"}}},
			}},
			ScopeSpans: []*tracepb.ScopeSpans{{
				Spans: []*tracepb.Span{{
					TraceId:           []byte{0x5b, 0x8e, 0xff, 0xf7, 0x98, 0x03, 0x81, 0x03, 0xd2, 0x69, 0xb6, 0x33, 0x81, 0x3f, 0xc6, 0x0c},
					SpanId:            []byte{0xee, 0xe1, 0x9b, 0x7e, 0xc3, 0xc1, 0xb1, 0x74},
					Name:              "chat claude-sonnet",
					Kind:              tracepb.Span_SPAN_KIND_CLIENT,
					StartTimeUnixNano: 1750000000000000000,
					EndTimeUnixNano:   1750000001000000000,
					Status:            &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
				}},
			}},
		}},
	}
}

var (
	_ = collectorlogspb.ExportLogsServiceRequest{}
	_ = collectormetricspb.ExportMetricsServiceRequest{}
	_ = logspb.LogRecord{}
	_ = metricspb.Metric{}
)
