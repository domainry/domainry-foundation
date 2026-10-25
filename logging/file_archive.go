package logging

import (
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

const archiveDateLayout = "2006-01-02"

type fileArchiveConfig struct {
	directory     string
	serviceName   string
	maxFileBytes  int64
	retentionDays int
	maxTotalBytes int64
}

type archiveManager struct {
	config fileArchiveConfig
	now    func() time.Time
	match  *regexp.Regexp

	mu     sync.Mutex
	active map[string]bool
}

type rollingFileSink struct {
	manager *archiveManager
	logType string

	mu      sync.Mutex
	file    *os.File
	path    string
	day     string
	segment int
	size    int64
}

type routedCore struct {
	core      zapcore.Core
	logType   string
	inherited []zapcore.Field
}

func normalizedFileArchiveConfig(config Config) (fileArchiveConfig, error) {
	directory := strings.TrimSpace(config.FileDirectory)
	if directory == "" {
		if config.DisableConsole {
			return fileArchiveConfig{}, errors.New("logging requires console output or LOG_DIRECTORY")
		}
		return fileArchiveConfig{}, nil
	}
	maxFileSizeMB := config.FileMaxSizeMB
	if maxFileSizeMB == 0 {
		maxFileSizeMB = defaultFileMaxSizeMB
	}
	retentionDays := config.FileRetentionDays
	if retentionDays == 0 {
		retentionDays = defaultFileRetentionDays
	}
	maxTotalSizeMB := config.FileMaxTotalSizeMB
	if maxTotalSizeMB == 0 {
		maxTotalSizeMB = defaultFileMaxTotalSizeMB
	}
	if maxFileSizeMB < 0 || retentionDays < 0 || maxTotalSizeMB < 0 {
		return fileArchiveConfig{}, errors.New("logging file limits must be positive")
	}
	if maxTotalSizeMB < maxFileSizeMB {
		return fileArchiveConfig{}, errors.New("LOG_MAX_TOTAL_SIZE_MB must be at least LOG_MAX_FILE_SIZE_MB")
	}
	serviceName := safeFileComponent(config.ServiceName)
	if serviceName == "" {
		return fileArchiveConfig{}, errors.New("logging service name must contain a file-safe character")
	}
	return fileArchiveConfig{
		directory:     directory,
		serviceName:   serviceName,
		maxFileBytes:  int64(maxFileSizeMB) * 1024 * 1024,
		retentionDays: retentionDays,
		maxTotalBytes: int64(maxTotalSizeMB) * 1024 * 1024,
	}, nil
}

func safeFileComponent(value string) string {
	value = strings.TrimSpace(value)
	var result strings.Builder
	for _, character := range value {
		switch {
		case character >= 'a' && character <= 'z', character >= 'A' && character <= 'Z', character >= '0' && character <= '9', character == '.', character == '_', character == '-':
			result.WriteRune(character)
		default:
			result.WriteByte('_')
		}
	}
	return strings.Trim(result.String(), ".")
}

func buildLogger(config Config, zapConfig zap.Config) (*zap.Logger, error) {
	archiveConfig, err := normalizedFileArchiveConfig(config)
	if err != nil {
		return nil, err
	}
	cores := make([]zapcore.Core, 0, 9)
	if !config.DisableConsole {
		consoleSink, closeConsole, openErr := zap.Open(zapConfig.OutputPaths...)
		if openErr != nil {
			return nil, openErr
		}
		consoleEncoder := zapcore.NewJSONEncoder(zapConfig.EncoderConfig)
		if zapConfig.Encoding == "console" {
			consoleEncoder = zapcore.NewConsoleEncoder(zapConfig.EncoderConfig)
		}
		cores = append(cores, routedOutputCores(consoleEncoder, consoleSink, zapConfig)...)
		_ = closeConsole
	}
	if archiveConfig.directory != "" {
		manager, managerErr := newArchiveManager(archiveConfig, time.Now)
		if managerErr != nil {
			return nil, managerErr
		}
		applicationSink := &rollingFileSink{manager: manager, logType: LogTypeApplication}
		accessSink := &rollingFileSink{manager: manager, logType: LogTypeAccess}
		infrastructureSink := &rollingFileSink{manager: manager, logType: LogTypeInfrastructure}
		fileEncoder := zapcore.NewJSONEncoder(zapConfig.EncoderConfig)
		cores = append(cores, routedApplicationCores(fileEncoder, zapcore.AddSync(applicationSink), zapConfig)...)
		cores = append(cores, newRoutedCore(zapcore.NewCore(fileEncoder.Clone(), zapcore.AddSync(accessSink), zap.LevelEnablerFunc(boundaryLevelEnabler)), LogTypeAccess))
		cores = append(cores, newRoutedCore(zapcore.NewCore(fileEncoder.Clone(), zapcore.AddSync(infrastructureSink), zap.LevelEnablerFunc(boundaryLevelEnabler)), LogTypeInfrastructure))
	}
	if len(cores) == 0 {
		return nil, errors.New("logging has no configured output")
	}
	errorSink, closeError, err := zap.Open(zapConfig.ErrorOutputPaths...)
	if err != nil {
		return nil, err
	}
	_ = closeError
	options := []zap.Option{zap.ErrorOutput(errorSink)}
	if !zapConfig.DisableCaller {
		options = append(options, zap.AddCaller())
	}
	if zapConfig.Development {
		options = append(options, zap.Development())
	}
	initialFields := make([]zap.Field, 0, len(zapConfig.InitialFields))
	keys := make([]string, 0, len(zapConfig.InitialFields))
	for key := range zapConfig.InitialFields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		initialFields = append(initialFields, zap.Any(key, zapConfig.InitialFields[key]))
	}
	if len(initialFields) > 0 {
		options = append(options, zap.Fields(initialFields...))
	}
	return zap.New(zapcore.NewTee(cores...), options...), nil
}

func routedOutputCores(encoder zapcore.Encoder, sink zapcore.WriteSyncer, config zap.Config) []zapcore.Core {
	cores := routedApplicationCores(encoder, sink, config)
	cores = append(cores, newRoutedCore(zapcore.NewCore(encoder.Clone(), sink, zap.LevelEnablerFunc(boundaryLevelEnabler)), LogTypeAccess))
	return append(cores, newRoutedCore(zapcore.NewCore(encoder.Clone(), sink, zap.LevelEnablerFunc(boundaryLevelEnabler)), LogTypeInfrastructure))
}

func routedApplicationCores(encoder zapcore.Encoder, sink zapcore.WriteSyncer, config zap.Config) []zapcore.Core {
	applicationBelowError := newRoutedCore(zapcore.NewCore(encoder.Clone(), sink, zap.LevelEnablerFunc(func(level zapcore.Level) bool {
		return config.Level.Enabled(level) && level < zapcore.ErrorLevel
	})), LogTypeApplication)
	if config.Sampling != nil {
		applicationBelowError = zapcore.NewSamplerWithOptions(applicationBelowError, time.Second, config.Sampling.Initial, config.Sampling.Thereafter)
	}
	applicationError := newRoutedCore(zapcore.NewCore(encoder.Clone(), sink, zap.LevelEnablerFunc(func(level zapcore.Level) bool {
		return config.Level.Enabled(level) && level >= zapcore.ErrorLevel
	})), LogTypeApplication)
	return []zapcore.Core{applicationBelowError, applicationError}
}

func boundaryLevelEnabler(level zapcore.Level) bool {
	return level >= zapcore.DebugLevel
}

func newRoutedCore(core zapcore.Core, logType string) zapcore.Core {
	if logType == LogTypeApplication {
		core = core.With([]zapcore.Field{zap.String(LogTypeField, LogTypeApplication)})
	}
	return &routedCore{core: core, logType: logType}
}

func (core *routedCore) Enabled(level zapcore.Level) bool {
	return core.core.Enabled(level)
}

func (core *routedCore) With(fields []zapcore.Field) zapcore.Core {
	inherited := append(append([]zapcore.Field(nil), core.inherited...), fields...)
	return &routedCore{core: core.core.With(fields), logType: core.logType, inherited: inherited}
}

func (core *routedCore) Check(entry zapcore.Entry, checked *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if !core.Enabled(entry.Level) || classifiedLogType(core.inherited) != core.logType {
		return checked
	}
	return checked.AddCore(entry, core)
}

func (core *routedCore) Write(entry zapcore.Entry, fields []zapcore.Field) error {
	combined := append(append([]zapcore.Field(nil), core.inherited...), fields...)
	if classifiedLogType(combined) != core.logType {
		return nil
	}
	return core.core.Write(entry, fields)
}

func (core *routedCore) Sync() error {
	return core.core.Sync()
}

func classifiedLogType(fields []zapcore.Field) string {
	for index := len(fields) - 1; index >= 0; index-- {
		field := fields[index]
		if field.Key != LogTypeField || field.Type != zapcore.StringType {
			continue
		}
		switch field.String {
		case LogTypeAccess:
			return LogTypeAccess
		case LogTypeInfrastructure:
			return LogTypeInfrastructure
		default:
			return LogTypeApplication
		}
	}
	return LogTypeApplication
}

func newArchiveManager(config fileArchiveConfig, now func() time.Time) (*archiveManager, error) {
	if err := os.MkdirAll(config.directory, 0o750); err != nil {
		return nil, fmt.Errorf("create logging directory: %w", err)
	}
	manager := &archiveManager{
		config: config,
		now:    now,
		match:  regexp.MustCompile(`^` + regexp.QuoteMeta(config.serviceName) + `\.(application|access|infrastructure)\.(\d{4}-\d{2}-\d{2})(?:\.(\d+))?\.log(?:\.gz|\.gz\.tmp)?$`),
		active: make(map[string]bool),
	}
	if err := manager.maintain(); err != nil {
		return nil, err
	}
	return manager, nil
}

func (sink *rollingFileSink) Write(content []byte) (int, error) {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if err := sink.ensureFile(int64(len(content))); err != nil {
		return 0, err
	}
	written, err := sink.file.Write(content)
	sink.size += int64(written)
	return written, err
}

func (sink *rollingFileSink) Sync() error {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if sink.file == nil {
		return nil
	}
	return sink.file.Sync()
}

func (sink *rollingFileSink) ensureFile(incoming int64) error {
	day := sink.manager.now().UTC().Format(archiveDateLayout)
	if sink.file != nil && (sink.day != day || (sink.size > 0 && sink.size+incoming > sink.manager.config.maxFileBytes)) {
		if err := sink.rotate(); err != nil {
			return err
		}
		if sink.day != day {
			sink.segment = 0
		} else {
			sink.segment++
		}
	}
	if sink.file != nil {
		return nil
	}
	sink.day = day
	segment, size, err := sink.currentSegment(day)
	if err != nil {
		return err
	}
	sink.segment, sink.size = segment, size
	if sink.size > 0 && sink.size+incoming > sink.manager.config.maxFileBytes {
		sink.segment++
		sink.size = 0
	}
	path := sink.filePath(day, sink.segment)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open %s log archive: %w", sink.logType, err)
	}
	sink.file, sink.path = file, path
	sink.manager.setActive(path, true)
	return nil
}

func (sink *rollingFileSink) rotate() error {
	path := sink.path
	if err := sink.file.Close(); err != nil {
		return fmt.Errorf("close %s log archive: %w", sink.logType, err)
	}
	sink.manager.setActive(path, false)
	sink.file, sink.path, sink.size = nil, "", 0
	if err := compressLogFile(path); err != nil {
		return err
	}
	return sink.manager.maintain()
}

func (sink *rollingFileSink) currentSegment(day string) (int, int64, error) {
	entries, err := os.ReadDir(sink.manager.config.directory)
	if err != nil {
		return 0, 0, err
	}
	prefix := sink.manager.config.serviceName + "." + sink.logType + "." + day
	segment, size := 0, int64(0)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), prefix) || (!strings.HasSuffix(entry.Name(), ".log") && !strings.HasSuffix(entry.Name(), ".log.gz")) {
			continue
		}
		match := sink.manager.match.FindStringSubmatch(entry.Name())
		if len(match) == 0 || match[1] != sink.logType || match[2] != day {
			continue
		}
		candidate := 0
		if match[3] != "" {
			candidate, _ = strconv.Atoi(match[3])
		}
		if candidate < segment {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return 0, 0, infoErr
		}
		segment, size = candidate, info.Size()
		if strings.HasSuffix(entry.Name(), ".gz") {
			size = sink.manager.config.maxFileBytes
		}
	}
	return segment, size, nil
}

func (sink *rollingFileSink) filePath(day string, segment int) string {
	name := sink.manager.config.serviceName + "." + sink.logType + "." + day
	if segment > 0 {
		name += "." + strconv.Itoa(segment)
	}
	return filepath.Join(sink.manager.config.directory, name+".log")
}

func (manager *archiveManager) setActive(path string, active bool) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if active {
		manager.active[path] = true
	} else {
		delete(manager.active, path)
	}
}

func (manager *archiveManager) maintain() error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	entries, err := os.ReadDir(manager.config.directory)
	if err != nil {
		return fmt.Errorf("read logging directory: %w", err)
	}
	today := manager.now().UTC().Format(archiveDateLayout)
	cutoff := manager.now().UTC().AddDate(0, 0, -manager.config.retentionDays).Format(archiveDateLayout)
	for _, entry := range entries {
		match := manager.match.FindStringSubmatch(entry.Name())
		if entry.IsDir() || len(match) == 0 {
			continue
		}
		path := filepath.Join(manager.config.directory, entry.Name())
		if strings.HasSuffix(entry.Name(), ".gz.tmp") {
			if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				return removeErr
			}
			continue
		}
		if match[2] < cutoff {
			if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				return fmt.Errorf("remove expired log archive: %w", removeErr)
			}
			continue
		}
		if match[2] < today && strings.HasSuffix(entry.Name(), ".log") && !manager.active[path] {
			if compressErr := compressLogFile(path); compressErr != nil {
				return compressErr
			}
		}
	}
	return manager.enforceTotalLimit()
}

func (manager *archiveManager) enforceTotalLimit() error {
	entries, err := os.ReadDir(manager.config.directory)
	if err != nil {
		return err
	}
	type candidate struct {
		path string
		name string
		day  string
		size int64
	}
	var total int64
	removable := make([]candidate, 0, len(entries))
	for _, entry := range entries {
		match := manager.match.FindStringSubmatch(entry.Name())
		if entry.IsDir() || len(match) == 0 || strings.HasSuffix(entry.Name(), ".tmp") {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return infoErr
		}
		path := filepath.Join(manager.config.directory, entry.Name())
		total += info.Size()
		if !manager.active[path] {
			removable = append(removable, candidate{path: path, name: entry.Name(), day: match[2], size: info.Size()})
		}
	}
	sort.Slice(removable, func(i, j int) bool {
		if removable[i].day == removable[j].day {
			return removable[i].name < removable[j].name
		}
		return removable[i].day < removable[j].day
	})
	for _, file := range removable {
		if total <= manager.config.maxTotalBytes {
			break
		}
		if err := os.Remove(file.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove log archive above total limit: %w", err)
		}
		total -= file.size
	}
	return nil
}

func compressLogFile(path string) error {
	if strings.HasSuffix(path, ".gz") {
		return nil
	}
	if _, err := os.Stat(path + ".gz"); err == nil {
		return os.Remove(path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	source, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("open log archive for compression: %w", err)
	}
	temporary := path + ".gz.tmp"
	destination, err := os.OpenFile(temporary, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		_ = source.Close()
		return fmt.Errorf("create compressed log archive: %w", err)
	}
	compressed := gzip.NewWriter(destination)
	_, copyErr := io.Copy(compressed, source)
	closeErr := errors.Join(compressed.Close(), destination.Close(), source.Close())
	if err := errors.Join(copyErr, closeErr); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("compress log archive: %w", err)
	}
	if err := os.Rename(temporary, path+".gz"); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("publish compressed log archive: %w", err)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove uncompressed log archive: %w", err)
	}
	return nil
}
