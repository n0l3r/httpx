// Package tracing provides OpenTelemetry tracing integration for httpx.
package tracing

import (
	"fmt"
	"net/http"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

const tracerName = "github.com/n0l3r/httpx"

// Transport wraps an http.RoundTripper with OpenTelemetry tracing.
// It creates a child span for each request and propagates trace context headers.
type Transport struct {
	// Tracer is the OTel tracer to use. Defaults to the global tracer.
	Tracer trace.Tracer
	// Propagator is the text map propagator. Defaults to the global propagator.
	Propagator propagation.TextMapPropagator
	// Base is the underlying transport.
	Base http.RoundTripper
	// SpanNameFormatter formats the span name from the request.
	// Default: "HTTP <METHOD>"
	SpanNameFormatter func(req *http.Request) string
}

func (t *Transport) tracer() trace.Tracer {
	if t.Tracer != nil {
		return t.Tracer
	}
	return otel.GetTracerProvider().Tracer(tracerName)
}

func (t *Transport) propagator() propagation.TextMapPropagator {
	if t.Propagator != nil {
		return t.Propagator
	}
	return otel.GetTextMapPropagator()
}

func (t *Transport) base() http.RoundTripper {
	if t.Base != nil {
		return t.Base
	}
	return http.DefaultTransport
}

func (t *Transport) spanName(req *http.Request) string {
	if t.SpanNameFormatter != nil {
		return t.SpanNameFormatter(req)
	}
	return fmt.Sprintf("HTTP %s", req.Method)
}

// RoundTrip creates an OTel span, injects trace context, and forwards the request.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx, span := t.tracer().Start(req.Context(), t.spanName(req),
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			semconv.HTTPRequestMethodKey.String(req.Method),
			semconv.URLFull(req.URL.String()),
			semconv.ServerAddress(req.URL.Host),
		),
	)
	defer span.End()

	// Inject trace context into request headers.
	r := req.Clone(ctx)
	t.propagator().Inject(ctx, propagation.HeaderCarrier(r.Header))

	resp, err := t.base().RoundTrip(r)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	span.SetAttributes(
		attribute.Int("http.response.status_code", resp.StatusCode),
	)

	if resp.StatusCode >= 400 {
		span.SetStatus(codes.Error, fmt.Sprintf("HTTP %d", resp.StatusCode))
	} else {
		span.SetStatus(codes.Ok, "")
	}

	return resp, nil
}
