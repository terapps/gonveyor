package gonveyor

import "log/slog"

// Logger is the package-level logger used by all gonveyor components.
var Logger = slog.Default()

// SetLogger replaces the package-level logger.
func SetLogger(l *slog.Logger) {
	Logger = l
}
