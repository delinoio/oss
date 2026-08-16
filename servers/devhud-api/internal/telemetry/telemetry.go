package telemetry

import (
	"context"
	"errors"
	"net/http"
	"os"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	otelprometheus "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"
)

type Providers struct {
	MeterProvider  *metric.MeterProvider
	TracerProvider *trace.TracerProvider
	MetricsHandler http.Handler
}

func New(ctx context.Context, serviceName, serviceVersion string) (*Providers, error) {
	registry := prometheus.NewRegistry()
	prometheusExporter, err := otelprometheus.New(otelprometheus.WithRegisterer(registry))
	if err != nil {
		return nil, err
	}
	serviceResource, err := resource.Merge(resource.Default(), resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName(serviceName),
		semconv.ServiceVersion(serviceVersion),
	))
	if err != nil {
		return nil, err
	}
	meterProvider := metric.NewMeterProvider(metric.WithReader(prometheusExporter), metric.WithResource(serviceResource))
	otel.SetMeterProvider(meterProvider)

	tracerOptions := []trace.TracerProviderOption{trace.WithResource(serviceResource)}
	if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "" {
		exporter, err := otlptracehttp.New(ctx)
		if err != nil {
			_ = meterProvider.Shutdown(ctx)
			return nil, err
		}
		tracerOptions = append(tracerOptions, trace.WithBatcher(exporter))
	}
	tracerProvider := trace.NewTracerProvider(tracerOptions...)
	otel.SetTracerProvider(tracerProvider)

	return &Providers{
		MeterProvider:  meterProvider,
		TracerProvider: tracerProvider,
		MetricsHandler: promhttp.HandlerFor(registry, promhttp.HandlerOpts{}),
	}, nil
}

func (p *Providers) Shutdown(ctx context.Context) error {
	return errors.Join(p.TracerProvider.Shutdown(ctx), p.MeterProvider.Shutdown(ctx))
}
