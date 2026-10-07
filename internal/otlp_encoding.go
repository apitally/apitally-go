package internal

import (
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
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
	return &logspb.LogRecord{
		TimeUnixNano:         timestamp,
		ObservedTimeUnixNano: timestamp,
		EventName:            r.eventName,
		Body:                 encodeValue(r.eventBody),
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

func unixNano(t time.Time) uint64 {
	if t.IsZero() {
		return 0
	}
	return uint64(t.UnixNano())
}
