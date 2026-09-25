// Package otlp decodes OpenTelemetry HTTP payloads and normalizes them into the
// Traceboard event contract. Decoding detects JSON versus protobuf from the body
// rather than trusting the Content-Type header alone, and enforces a
// decompressed size ceiling so a small compressed body cannot expand without
// bound.
package otlp

import (
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	collectorlogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	"io"
	"strings"

	collectormetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	collectortracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type Kind string

const (
	KindTraces  Kind = "traces"
	KindLogs    Kind = "logs"
	KindMetrics Kind = "metrics"
)

// MaxDecompressedBytes is the compiled ceiling for one decoded OTLP body.
const MaxDecompressedBytes = 8 << 20

type Signal struct {
	Kind     Kind
	Traces   *collectortracepb.ExportTraceServiceRequest
	Logs     *collectorlogspb.ExportLogsServiceRequest
	Metrics  *collectormetricspb.ExportMetricsServiceRequest
	Encoding string
}

var (
	ErrPayloadTooLarge = errors.New("otlp payload exceeds the decompressed size limit")
	ErrUnsupportedKind = errors.New("unsupported otlp signal")
	ErrMalformed       = errors.New("otlp payload could not be decoded")
)

func Decode(kind Kind, contentType string, body []byte, limit int64) (Signal, error) {
	if limit <= 0 || limit > MaxDecompressedBytes {
		limit = MaxDecompressedBytes
	}
	payload, encoding, err := decompress(contentType, body, limit)
	if err != nil {
		return Signal{}, err
	}

	signal := Signal{Kind: kind, Encoding: encoding}
	switch kind {
	case KindTraces:
		signal.Traces = &collectortracepb.ExportTraceServiceRequest{}
	case KindLogs:
		signal.Logs = &collectorlogspb.ExportLogsServiceRequest{}
	case KindMetrics:
		signal.Metrics = &collectormetricspb.ExportMetricsServiceRequest{}
	default:
		return Signal{}, ErrUnsupportedKind
	}

	var target proto.Message
	switch kind {
	case KindTraces:
		target = signal.Traces
	case KindLogs:
		target = signal.Logs
	case KindMetrics:
		target = signal.Metrics
	}

	if encoding == "json" {
		decoder := protojson.UnmarshalOptions{DiscardUnknown: true}
		if err := decoder.Unmarshal(payload, target); err != nil {
			return Signal{}, fmt.Errorf("%w: %v", ErrMalformed, err)
		}
		return signal, nil
	}
	if err := proto.Unmarshal(payload, target); err != nil {
		return Signal{}, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	return signal, nil
}

func decompress(contentType string, body []byte, limit int64) ([]byte, string, error) {
	mediaType, parameters := parseContentType(contentType)
	compressed := strings.Contains(mediaType, "gzip") || strings.EqualFold(parameters["encoding"], "gzip") || looksLikeGzip(body)
	if compressed {
		reader, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			return nil, "", fmt.Errorf("%w: %v", ErrMalformed, err)
		}
		defer reader.Close()
		decompressed, err := readLimited(reader, limit)
		if err != nil {
			return nil, "", err
		}
		body = decompressed
	}
	if int64(len(body)) > limit {
		return nil, "", ErrPayloadTooLarge
	}
	encoding := "protobuf"
	if looksLikeJSON(body) {
		encoding = "json"
	}
	return body, encoding, nil
}

func readLimited(reader io.Reader, limit int64) ([]byte, error) {
	limited := io.LimitReader(reader, limit+1)
	payload, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	if int64(len(payload)) > limit {
		return nil, ErrPayloadTooLarge
	}
	return payload, nil
}

// looksLikeGzip sniffs the gzip magic number. Some exporters compress the body
// without advertising it, and a compressed body never decodes as protobuf.
func looksLikeGzip(body []byte) bool {
	return len(body) >= 2 && body[0] == 0x1f && body[1] == 0x8b
}

// looksLikeJSON sniffs the first meaningful byte. A JSON body is never a valid
// protobuf message, so this cannot silently mis-decode.
func looksLikeJSON(body []byte) bool {
	for _, b := range body {
		switch b {
		case ' ', '\t', '\r', '\n':
			continue
		case '{', '[':
			return true
		default:
			return false
		}
	}
	return false
}

func parseContentType(value string) (string, map[string]string) {
	parameters := map[string]string{}
	parts := strings.Split(value, ";")
	mediaType := strings.ToLower(strings.TrimSpace(parts[0]))
	for _, part := range parts[1:] {
		key, raw, found := strings.Cut(part, "=")
		if !found {
			continue
		}
		parameters[strings.ToLower(strings.TrimSpace(key))] = strings.Trim(strings.TrimSpace(raw), `"`)
	}
	return mediaType, parameters
}

// EmptySuccess returns the protobuf response body an OTLP exporter expects for
// a fully or partially accepted request.
func EmptySuccess(kind Kind) []byte {
	switch kind {
	case KindTraces:
		encoded, _ := proto.Marshal(&collectortracepb.ExportTraceServiceResponse{})
		return encoded
	case KindLogs:
		encoded, _ := proto.Marshal(&collectorlogspb.ExportLogsServiceResponse{})
		return encoded
	case KindMetrics:
		encoded, _ := proto.Marshal(&collectormetricspb.ExportMetricsServiceResponse{})
		return encoded
	default:
		return nil
	}
}

func AnyValueToMap(value *commonpb.AnyValue) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	result := map[string]any{}
	switch typed := value.Value.(type) {
	case *commonpb.AnyValue_StringValue:
		result["string"] = typed.StringValue
	case *commonpb.AnyValue_BoolValue:
		result["bool"] = typed.BoolValue
	case *commonpb.AnyValue_IntValue:
		result["int"] = typed.IntValue
	case *commonpb.AnyValue_DoubleValue:
		result["double"] = typed.DoubleValue
	case *commonpb.AnyValue_ArrayValue:
		items := make([]any, 0, len(typed.ArrayValue.Values))
		for _, item := range typed.ArrayValue.Values {
			items = append(items, AnyValueToMap(item)["string"])
		}
		result["array"] = items
	case *commonpb.AnyValue_KvlistValue:
		nested := map[string]any{}
		for _, pair := range typed.KvlistValue.Values {
			nested[pair.Key] = scalar(AnyValueToMap(pair.Value))
		}
		result["kvlist"] = nested
	}
	return result
}

func scalar(value map[string]any) any {
	if len(value) != 1 {
		return value
	}
	for _, inner := range value {
		return inner
	}
	return nil
}

func attributesToMap(attributes []*commonpb.KeyValue) map[string]any {
	result := make(map[string]any, len(attributes))
	for _, attribute := range attributes {
		if attribute == nil {
			continue
		}
		result[attribute.Key] = scalar(AnyValueToMap(attribute.Value))
	}
	return result
}

func resourceAttributes(resource *resourcepb.Resource) map[string]any {
	if resource == nil {
		return map[string]any{}
	}
	return attributesToMap(resource.Attributes)
}

func severityName(value logspb.SeverityNumber) string {
	return strings.ToLower(strings.TrimPrefix(value.String(), "SEVERITY_NUMBER_"))
}
