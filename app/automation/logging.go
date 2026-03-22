package automation

import "log/slog"

func addRequestLoggerAttrs(requestID string, correlationID string, operationID string) {
	attrs := make([]slog.Attr, 0, 3)
	if requestID != "" {
		attrs = append(attrs, slog.String("request_id", requestID))
	}
	if correlationID != "" {
		attrs = append(attrs, slog.String("correlation_id", correlationID))
	}
	if operationID != "" {
		attrs = append(attrs, slog.String("operation_id", operationID))
	}
	if len(attrs) == 0 {
		return
	}
	slog.SetDefault(slog.New(slog.Default().Handler().WithAttrs(attrs)))
}
