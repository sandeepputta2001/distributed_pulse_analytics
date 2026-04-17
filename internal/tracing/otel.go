package tracing

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/pulse-analytics/internal/config"
)

// Provider holds the OpenTelemetry TracerProvider.
type Provider struct {
	tp  *sdktrace.TracerProvider
	log *zap.Logger
}

// Init initializes OpenTelemetry tracing with OTLP gRPC exporter.
func Init(ctx context.Context, cfg *config.TelemetryConfig, log *zap.Logger) (*Provider, error) {
	if !cfg.TracingEnabled {
		otel.SetTracerProvider(trace.NewNoopTracerProvider())
		return &Provider{log: log}, nil
	}

	conn, err := grpc.NewClient(cfg.OTLPEndpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("otlp grpc connect: %w", err)
	}

	exporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithGRPCConn(conn),
		otlptracegrpc.WithTimeout(10*time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("otlp trace exporter: %w", err)
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(cfg.ServiceName),
			semconv.ServiceVersion("1.0.0"),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("otel resource: %w", err)
	}

	// Tail-based: 100% for errors, configured ratio for normal traffic
	sampler := sdktrace.ParentBased(
		sdktrace.TraceIDRatioBased(cfg.SamplingRate),
	)

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter,
			sdktrace.WithBatchTimeout(5*time.Second),
			sdktrace.WithMaxExportBatchSize(512),
		),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sampler),
	)

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	log.Info("otel tracing initialized",
		zap.String("endpoint", cfg.OTLPEndpoint),
		zap.Float64("sampling_rate", cfg.SamplingRate),
	)

	return &Provider{tp: tp, log: log}, nil
}

// Tracer returns a named tracer.
func Tracer(name string) trace.Tracer {
	return otel.Tracer(name)
}

// Shutdown flushes pending spans.
func (p *Provider) Shutdown(ctx context.Context) error {
	if p.tp != nil {
		return p.tp.Shutdown(ctx)
	}
	return nil
}
