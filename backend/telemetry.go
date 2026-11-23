package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"time"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/contrib/exporters/autoexport"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	otelruntime "go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/trace"
)

// Development hosts can supply the same OTLP endpoint and credentials as the old app host.
func setupTelemetry(ctx context.Context, c Config, h http.Handler) (http.Handler, func(), error) {
	if !c.Development || os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" {
		return h, func() {}, nil
	}
	var shutdown []func(context.Context) error
	closeAll := func() {
		deadline, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var err error
		for _, stop := range shutdown {
			err = errors.Join(err, stop(deadline))
		}
		if err != nil {
			slog.Warn("flush telemetry", "error", err)
		}
	}
	exporter, err := autoexport.NewSpanExporter(ctx)
	if err != nil {
		return nil, nil, err
	}
	tracer := trace.NewTracerProvider(trace.WithBatcher(exporter))
	shutdown = append(shutdown, tracer.Shutdown)
	reader, err := autoexport.NewMetricReader(ctx)
	if err != nil {
		closeAll()
		return nil, nil, err
	}
	meter := metric.NewMeterProvider(metric.WithReader(reader))
	shutdown = append(shutdown, meter.Shutdown)
	logs, err := autoexport.NewLogExporter(ctx)
	if err != nil {
		closeAll()
		return nil, nil, err
	}
	logger := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewBatchProcessor(logs)))
	shutdown = append(shutdown, logger.Shutdown)
	otel.SetTracerProvider(tracer)
	otel.SetMeterProvider(meter)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	if err := otelruntime.Start(otelruntime.WithMeterProvider(meter)); err != nil {
		closeAll()
		return nil, nil, err
	}
	slog.SetDefault(slog.New(slog.NewMultiHandler(slog.Default().Handler(), otelslog.NewHandler("aptabase", otelslog.WithLoggerProvider(logger)))))
	// Clients with no explicit transport inherit outbound HTTP instrumentation.
	http.DefaultTransport = otelhttp.NewTransport(http.DefaultTransport)
	return otelhttp.NewHandler(h, "aptabase"), closeAll, nil
}
