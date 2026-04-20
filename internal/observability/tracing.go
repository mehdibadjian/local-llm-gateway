package observability

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

const TracerName = "caw"

// Tracer returns the package-level OTel tracer.
func Tracer() trace.Tracer {
	return otel.Tracer(TracerName)
}

// StartSpan is a convenience wrapper around Tracer().Start.
func StartSpan(ctx context.Context, name string) (context.Context, trace.Span) {
	return Tracer().Start(ctx, name)
}

// Canonical span names for the critical request path.
const (
	SpanContextLoad  = "caw.context-load"
	SpanRAGRetrieval = "caw.rag-retrieval"
	SpanInference    = "caw.inference"
	SpanCritique     = "caw.critique"
	SpanFormatRetry  = "caw.format-retry"
)
