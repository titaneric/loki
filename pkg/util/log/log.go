package log

import (
	"fmt"
	"io"
	"math"
	"os"
	"time"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/weaveworks/common/logging"
	"github.com/weaveworks/common/server"
)

var (
	// Logger is a shared go-kit logger.
	// TODO: Change all components to take a non-global logger via their constructors.
	// Prefer accepting a non-global logger as an argument.
	Logger = log.NewNopLogger()

	bufferedLogger *LineBufferedLogger
)

// InitLogger initialises the global gokit logger (util_log.Logger) and overrides the
// default logger for the server.
func InitLogger(cfg *server.Config, reg prometheus.Registerer, buffered bool, sync bool) {
	l := newPrometheusLogger(cfg.LogLevel, cfg.LogFormat, reg, buffered, sync)

	// when use util_log.Logger, skip 3 stack frames.
	Logger = log.With(l, "caller", log.Caller(3))

	// cfg.Log wraps log function, skip 4 stack frames to get caller information.
	// this works in go 1.12, but doesn't work in versions earlier.
	// it will always shows the wrapper function generated by compiler
	// marked <autogenerated> in old versions.
	cfg.Log = logging.GoKit(log.With(l, "caller", log.Caller(4)))
}

type Flusher interface {
	Flush() error
}

func Flush() error {
	if bufferedLogger != nil {
		return bufferedLogger.Flush()
	}

	return nil
}

// prometheusLogger exposes Prometheus counters for each of go-kit's log levels.
type prometheusLogger struct {
	logger              log.Logger
	logMessages         *prometheus.CounterVec
	internalLogMessages *prometheus.CounterVec
	logFlushes          prometheus.Histogram

	useBufferedLogger bool
	useSyncLogger     bool
}

// newPrometheusLogger creates a new instance of PrometheusLogger which exposes
// Prometheus counters for various log levels.
func newPrometheusLogger(l logging.Level, format logging.Format, reg prometheus.Registerer, buffered bool, sync bool) log.Logger {

	// buffered logger settings
	var (
		logEntries    uint32 = 256                    // buffer up to 256 log lines in memory before flushing to a write(2) syscall
		logBufferSize uint32 = 10e6                   // 10MB
		flushTimeout         = 100 * time.Millisecond // flush the buffer after 100ms regardless of how full it is, to prevent losing many logs in case of ungraceful termination
	)

	logMessages := promauto.With(reg).NewCounterVec(prometheus.CounterOpts{
		Namespace: "loki",
		Name:      "log_messages_total",
		Help:      "DEPRECATED. Use internal_log_messages_total for the same functionality. Total number of log messages created by Loki itself.",
	}, []string{"level"})
	internalLogMessages := promauto.With(reg).NewCounterVec(prometheus.CounterOpts{
		Namespace: "loki",
		Name:      "internal_log_messages_total",
		Help:      "Total number of log messages created by Loki itself.",
	}, []string{"level"})
	logFlushes := promauto.With(reg).NewHistogram(prometheus.HistogramOpts{
		Namespace: "loki",
		Name:      "log_flushes",
		Help:      "Histogram of log flushes using the line-buffered logger.",
		Buckets:   prometheus.ExponentialBuckets(1, 2, int(math.Log2(float64(logEntries)))+1),
	})

	var writer io.Writer
	if buffered {
		// retain a reference to this logger because it doesn't conform to the standard Logger interface,
		// and we can't unwrap it to get the underlying logger when we flush on shutdown
		bufferedLogger = NewLineBufferedLogger(os.Stderr, logEntries,
			WithFlushPeriod(flushTimeout),
			WithPrellocatedBuffer(logBufferSize),
			WithFlushCallback(func(entries uint32) {
				logFlushes.Observe(float64(entries))
			}),
		)

		writer = bufferedLogger
	} else {
		writer = os.Stderr
	}

	if sync {
		writer = log.NewSyncWriter(writer)
	}

	logger := log.NewLogfmtLogger(writer)
	if format.String() == "json" {
		logger = log.NewJSONLogger(writer)
	}
	logger = level.NewFilter(logger, levelFilter(l.String()))

	plogger := &prometheusLogger{
		logger:              logger,
		logMessages:         logMessages,
		internalLogMessages: internalLogMessages,
		logFlushes:          logFlushes,
	}
	// Initialise counters for all supported levels:
	supportedLevels := []level.Value{
		level.DebugValue(),
		level.InfoValue(),
		level.WarnValue(),
		level.ErrorValue(),
	}
	for _, level := range supportedLevels {
		plogger.logMessages.WithLabelValues(level.String())
		plogger.internalLogMessages.WithLabelValues(level.String())
	}

	// return a Logger without caller information, shouldn't use directly
	return log.With(plogger, "ts", log.DefaultTimestampUTC)
}

// Log increments the appropriate Prometheus counter depending on the log level.
func (pl *prometheusLogger) Log(kv ...interface{}) error {
	pl.logger.Log(kv...)
	l := "unknown"
	for i := 1; i < len(kv); i += 2 {
		if v, ok := kv[i].(level.Value); ok {
			l = v.String()
			break
		}
	}
	pl.logMessages.WithLabelValues(l).Inc()
	pl.internalLogMessages.WithLabelValues(l).Inc()
	return nil
}

// CheckFatal prints an error and exits with error code 1 if err is non-nil.
func CheckFatal(location string, err error, logger log.Logger) {
	if err == nil {
		return
	}

	logger = level.Error(logger)
	if location != "" {
		logger = log.With(logger, "msg", "error "+location)
	}
	// %+v gets the stack trace from errors using github.com/pkg/errors
	errStr := fmt.Sprintf("%+v", err)
	fmt.Fprintln(os.Stderr, errStr)

	logger.Log("err", errStr)
	if err = Flush(); err != nil {
		fmt.Fprintln(os.Stderr, "Could not flush logger", err)
	}
	os.Exit(1)
}

// TODO: remove once weaveworks/common updates to go-kit/log, we can then revert to using Level.Gokit
func levelFilter(l string) level.Option {
	switch l {
	case "debug":
		return level.AllowDebug()
	case "info":
		return level.AllowInfo()
	case "warn":
		return level.AllowWarn()
	case "error":
		return level.AllowError()
	default:
		return level.AllowAll()
	}
}
