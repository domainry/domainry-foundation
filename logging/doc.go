// Package logging owns one structured Zap root logger per process. It keeps
// severity as record metadata and routes application and HTTP access events to
// independent UTC daily, size-bounded archives when file logging is enabled.
package logging
