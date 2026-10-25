// Package logging owns one structured Zap root logger per process. It keeps
// severity as record metadata and routes application, HTTP access, and
// infrastructure dependency events to independent UTC daily, size-bounded
// archives when file logging is enabled.
package logging
