package internal

import (
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/protobuf/reflect/protoreflect"
)

const (
	sdkScopeName = "apitally"
	// Bounds each spool append, so no single append overshoots the file rotation threshold.
	recordsPerEncodedChunk = 32
)

func encodeResource(res *resource.Resource) *resourcepb.Resource {
	return &resourcepb.Resource{Attributes: encodeAttributes(res.Attributes())}
}

func encodeLogs(res *resourcepb.Resource, records []*logRecord) *logspb.LogsData {
	var scopes []*logspb.ScopeLogs
	for _, r := range records {
		scopeName := r.scopeName()
		i := 0
		for i < len(scopes) && scopes[i].Scope.Name != scopeName {
			i++
		}
		if i == len(scopes) {
			scopes = append(scopes, &logspb.ScopeLogs{Scope: &commonpb.InstrumentationScope{Name: scopeName}})
		}
		scopes[i].LogRecords = append(scopes[i].LogRecords, encodeLogRecord(r))
	}
	return &logspb.LogsData{ResourceLogs: []*logspb.ResourceLogs{{Resource: res, ScopeLogs: scopes}}}
}

func encodeLogRecord(r *logRecord) *logspb.LogRecord {
	timestamp := unixNano(r.Record.Time)
	if r.eventName != "" {
		return &logspb.LogRecord{TimeUnixNano: timestamp, ObservedTimeUnixNano: timestamp, EventName: r.eventName, Body: encodeValue(r.eventBody)}
	}
	attrs := []attribute.KeyValue{
		attribute.String("apitally.request.server_span_id", r.serverSpanID.String()),
		attribute.String("code.function.name", r.codeFunction),
		attribute.String("code.file.path", r.codeFile),
		attribute.Int("code.line.number", r.codeLine),
	}
	return &logspb.LogRecord{
		TimeUnixNano:         timestamp,
		ObservedTimeUnixNano: timestamp,
		SeverityNumber:       logspb.SeverityNumber(slogSeverityNumber(r.Record.Level)),
		SeverityText:         r.Record.Level.String(),
		Body:                 &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: r.Record.Message}},
		Attributes:           encodeAttributes(append(attrs, slogAttributes(r.Record.Attrs)...)),
		TraceId:              r.traceID[:],
		SpanId:               r.spanID[:],
		Flags:                uint32(r.traceFlags),
	}
}

func encodeProcessGauges(res *resourcepb.Resource, start, end time.Time, values processMetricValues) *metricspb.MetricsData {
	var metrics []*metricspb.Metric
	gauge := func(name, unit string, point *metricspb.NumberDataPoint) {
		point.StartTimeUnixNano, point.TimeUnixNano = unixNano(start), unixNano(end)
		metrics = append(metrics, &metricspb.Metric{
			Name: name,
			Unit: unit,
			Data: &metricspb.Metric_Gauge{Gauge: &metricspb.Gauge{DataPoints: []*metricspb.NumberDataPoint{point}}},
		})
	}
	if values.hasCPUUtilization {
		gauge("process.cpu.utilization", "1", &metricspb.NumberDataPoint{Value: &metricspb.NumberDataPoint_AsDouble{AsDouble: values.cpuUtilization}})
	}
	if values.hasMemoryUsage {
		gauge("process.memory.usage", "By", &metricspb.NumberDataPoint{Value: &metricspb.NumberDataPoint_AsInt{AsInt: values.memoryUsage}})
	}
	gauge("process.uptime", "s", &metricspb.NumberDataPoint{Value: &metricspb.NumberDataPoint_AsDouble{AsDouble: values.uptime}})
	return encodeMetrics(res, metrics)
}

func encodeRequestHistograms(res *resourcepb.Resource, start, end time.Time, keys []requestMetricKey, requests map[requestMetricKey]*requestMetricValues) *metricspb.MetricsData {
	histogram := func(name, unit string) *metricspb.Metric {
		return &metricspb.Metric{Name: name, Unit: unit, Data: &metricspb.Metric_ExponentialHistogram{ExponentialHistogram: &metricspb.ExponentialHistogram{
			AggregationTemporality: metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_DELTA,
		}}}
	}
	duration := histogram("http.server.request.duration", "s")
	requestBodySize := histogram("http.server.request.body.size", "By")
	responseBodySize := histogram("http.server.response.body.size", "By")
	addPoint := func(metric *metricspb.Metric, h *exponentialHistogram, attrs []*commonpb.KeyValue) {
		if h.count > 0 {
			point := h.dataPoint()
			point.StartTimeUnixNano, point.TimeUnixNano, point.Attributes = unixNano(start), unixNano(end), attrs
			metric.GetExponentialHistogram().DataPoints = append(metric.GetExponentialHistogram().DataPoints, point)
		}
	}
	for _, key := range keys {
		attrs := []attribute.KeyValue{
			attribute.String("http.request.method", key.method),
			attribute.String("http.route", key.route),
			attribute.Int("http.response.status_code", key.statusCode),
			attribute.String("url.scheme", key.scheme),
		}
		if key.consumer != "" {
			attrs = append(attrs, attribute.String("apitally.consumer.identifier", key.consumer))
		}
		if key.statusCode >= 500 {
			attrs = append(attrs, attribute.String("error.type", strconv.Itoa(key.statusCode)))
		}
		encoded := encodeAttributes(attrs)
		values := requests[key]
		addPoint(duration, &values.duration, encoded)
		addPoint(requestBodySize, &values.requestBodySize, encoded)
		addPoint(responseBodySize, &values.responseBodySize, encoded)
	}
	var metrics []*metricspb.Metric
	for _, metric := range []*metricspb.Metric{duration, requestBodySize, responseBodySize} {
		if len(metric.GetExponentialHistogram().DataPoints) > 0 {
			metrics = append(metrics, metric)
		}
	}
	return encodeMetrics(res, metrics)
}

func encodeMetrics(res *resourcepb.Resource, metrics []*metricspb.Metric) *metricspb.MetricsData {
	return &metricspb.MetricsData{ResourceMetrics: []*metricspb.ResourceMetrics{{
		Resource:     res,
		ScopeMetrics: []*metricspb.ScopeMetrics{{Scope: &commonpb.InstrumentationScope{Name: sdkScopeName}, Metrics: metrics}},
	}}}
}

func encodeAttributes(attrs []attribute.KeyValue) []*commonpb.KeyValue {
	if len(attrs) == 0 {
		return nil
	}
	out := make([]*commonpb.KeyValue, len(attrs))
	for i, kv := range attrs {
		out[i] = &commonpb.KeyValue{Key: string(kv.Key), Value: encodeValue(kv.Value)}
	}
	return out
}

// encodeValue matches the otlptrace exporter's attribute conversion. An
// EMPTY value encodes as an AnyValue without a value, which is OTLP's null.
func encodeValue(v attribute.Value) *commonpb.AnyValue {
	av := &commonpb.AnyValue{}
	switch v.Type() {
	case attribute.BOOL:
		av.Value = &commonpb.AnyValue_BoolValue{BoolValue: v.AsBool()}
	case attribute.INT64:
		av.Value = &commonpb.AnyValue_IntValue{IntValue: v.AsInt64()}
	case attribute.FLOAT64:
		av.Value = &commonpb.AnyValue_DoubleValue{DoubleValue: v.AsFloat64()}
	case attribute.STRING:
		av.Value = &commonpb.AnyValue_StringValue{StringValue: v.AsString()}
	case attribute.BYTESLICE:
		av.Value = &commonpb.AnyValue_BytesValue{BytesValue: v.AsByteSlice()}
	case attribute.BOOLSLICE:
		av.Value = encodeArray(v.AsBoolSlice(), attribute.BoolValue)
	case attribute.INT64SLICE:
		av.Value = encodeArray(v.AsInt64Slice(), attribute.Int64Value)
	case attribute.FLOAT64SLICE:
		av.Value = encodeArray(v.AsFloat64Slice(), attribute.Float64Value)
	case attribute.STRINGSLICE:
		av.Value = encodeArray(v.AsStringSlice(), attribute.StringValue)
	case attribute.SLICE:
		av.Value = encodeArray(v.AsSlice(), func(v attribute.Value) attribute.Value { return v })
	case attribute.MAP:
		av.Value = &commonpb.AnyValue_KvlistValue{KvlistValue: &commonpb.KeyValueList{Values: encodeAttributes(v.AsMap())}}
	}
	return av
}

func encodeArray[T any](items []T, toValue func(T) attribute.Value) *commonpb.AnyValue_ArrayValue {
	values := make([]*commonpb.AnyValue, len(items))
	for i, item := range items {
		values[i] = encodeValue(toValue(item))
	}
	return &commonpb.AnyValue_ArrayValue{ArrayValue: &commonpb.ArrayValue{Values: values}}
}

// replaceInvalidUTF8 replaces invalid UTF-8 in every string field of m and
// its nested messages, which OTLP requires to be valid UTF-8.
func replaceInvalidUTF8(m protoreflect.Message) {
	m.Range(func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		switch {
		case field.IsList():
			list := value.List()
			for i := range list.Len() {
				switch field.Kind() {
				case protoreflect.MessageKind:
					replaceInvalidUTF8(list.Get(i).Message())
				case protoreflect.StringKind:
					list.Set(i, protoreflect.ValueOfString(strings.ToValidUTF8(list.Get(i).String(), "\uFFFD")))
				}
			}
		case field.Kind() == protoreflect.MessageKind:
			replaceInvalidUTF8(value.Message())
		case field.Kind() == protoreflect.StringKind:
			m.Set(field, protoreflect.ValueOfString(strings.ToValidUTF8(value.String(), "\uFFFD")))
		}
		return true
	})
}

func unixNano(t time.Time) uint64 {
	if t.IsZero() {
		return 0
	}
	return uint64(t.UnixNano())
}
