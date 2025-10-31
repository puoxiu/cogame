package logger

import (
	"fmt"
	"os"
	"sync"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var (
	globalLogger *Logger
	once         sync.Once
)

// Logger 高性能日志记录器
type Logger struct {
	*zap.Logger
	sugar  *zap.SugaredLogger
	fields []zap.Field
	mutex  sync.RWMutex
}

// LogConfig 日志配置
type LogConfig struct {
	Level             string          `mapstructure:"level"`              // 日志级别
	Format            string          `mapstructure:"format"`             // 日志格式 json/console
	Output            string          `mapstructure:"output"`             // 输出 stdout/stderr/file
	FilePath          string          `mapstructure:"file_path"`          // 文件路径
	MaxSize           int             `mapstructure:"max_size"`           // 最大文件大小(MB)
	MaxBackups        int             `mapstructure:"max_backups"`        // 最大备份数
	MaxAge            int             `mapstructure:"max_age"`            // 最大保存天数
	Compress          bool            `mapstructure:"compress"`           // 是否压缩
	Development       bool            `mapstructure:"development"`        // 开发模式
	DisableCaller     bool            `mapstructure:"disable_caller"`     // 禁用调用者信息
	DisableStacktrace bool            `mapstructure:"disable_stacktrace"` // 禁用堆栈跟踪
	Sampling          *SamplingConfig `mapstructure:"sampling"`           // 采样配置
}

// SamplingConfig 采样配置
type SamplingConfig struct {
	Initial    int `mapstructure:"initial"`    // 初始采样数
	Thereafter int `mapstructure:"thereafter"` // 后续采样率
}

// NewLogger 创建新的日志记录器
func NewLogger(config *LogConfig) *Logger {
	// 解析日志级别
	level := parseLogLevel(config.Level)

	// 创建编码器配置
	encoderConfig := getEncoderConfig(config.Development)

	// 创建编码器
	var encoder zapcore.Encoder
	if config.Format == "json" {
		encoder = zapcore.NewJSONEncoder(encoderConfig)
	} else {
		encoder = zapcore.NewConsoleEncoder(encoderConfig)
	}

	// 创建写入器
	writeSyncer := getLogWriter(config)

	// 创建核心
	core := zapcore.NewCore(encoder, writeSyncer, level)

	// 创建zap选项
	opts := buildLoggerOptions(config)

	// 创建zap logger
	zapLogger := zap.New(core, opts...)

	logger := &Logger{
		Logger: zapLogger,
		sugar:  zapLogger.Sugar(),
		fields: make([]zap.Field, 0),
	}

	return logger
}

// parseLogLevel 解析日志级别
func parseLogLevel(level string) zapcore.Level {
	switch level {
	case "debug":
		return zapcore.DebugLevel
	case "info":
		return zapcore.InfoLevel
	case "warn", "warning":
		return zapcore.WarnLevel
	case "error":
		return zapcore.ErrorLevel
	case "fatal":
		return zapcore.FatalLevel
	case "panic":
		return zapcore.PanicLevel
	default:
		return zapcore.InfoLevel
	}
}

// getEncoderConfig 获取编码器配置
func getEncoderConfig(development bool) zapcore.EncoderConfig {
	if development {
		config := zap.NewDevelopmentEncoderConfig()
		config.EncodeTime = zapcore.TimeEncoderOfLayout("2006-01-02 15:04:05.000")
		config.EncodeLevel = zapcore.CapitalColorLevelEncoder
		return config
	}

	config := zap.NewProductionEncoderConfig()
	config.TimeKey = "timestamp"
	config.LevelKey = "level"
	config.MessageKey = "message"
	config.CallerKey = "caller"
	config.StacktraceKey = "stacktrace"
	config.EncodeTime = zapcore.ISO8601TimeEncoder
	config.EncodeLevel = zapcore.LowercaseLevelEncoder
	config.EncodeDuration = zapcore.SecondsDurationEncoder
	config.EncodeCaller = zapcore.ShortCallerEncoder

	return config
}

// getLogWriter 获取日志写入器
func getLogWriter(config *LogConfig) zapcore.WriteSyncer {
	switch config.Output {
	case "stderr":
		return zapcore.AddSync(os.Stderr)
	case "file":
		if config.FilePath != "" {
			// 使用轮转日志文件
			return getRotatingFileWriter(config)
		}
		fallthrough
	default:
		return zapcore.AddSync(os.Stdout)
	}
}

// getRotatingFileWriter 获取轮转文件写入器
func getRotatingFileWriter(config *LogConfig) zapcore.WriteSyncer {
	// 这里可以集成lumberjack或其他日志轮转库
	// 简化实现，直接写入文件
	file, err := os.OpenFile(config.FilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		return zapcore.AddSync(os.Stdout)
	}
	return zapcore.AddSync(file)
}

// buildLoggerOptions 构建日志器选项
func buildLoggerOptions(config *LogConfig) []zap.Option {
	opts := make([]zap.Option, 0)

	// 添加调用者信息
	if !config.DisableCaller {
		opts = append(opts, zap.AddCaller(), zap.AddCallerSkip(1))
	}

	// 添加堆栈跟踪
	if !config.DisableStacktrace {
		opts = append(opts, zap.AddStacktrace(zapcore.ErrorLevel))
	}

	// 采样配置
	if config.Sampling != nil {
		sampling := &zap.SamplingConfig{
			Initial:    config.Sampling.Initial,
			Thereafter: config.Sampling.Thereafter,
		}
		opts = append(opts, zap.WrapCore(func(core zapcore.Core) zapcore.Core {
			return zapcore.NewSamplerWithOptions(core, time.Second, sampling.Initial, sampling.Thereafter)
		}))
	}

	// 开发模式
	if config.Development {
		opts = append(opts, zap.Development())
	}

	return opts
}

// WithField 添加字段
func (l *Logger) WithField(key string, value interface{}) *Logger {
	l.mutex.Lock()
	defer l.mutex.Unlock()

	newFields := make([]zap.Field, len(l.fields)+1)
	copy(newFields, l.fields)
	newFields[len(l.fields)] = zap.Any(key, value)

	return &Logger{
		Logger: l.Logger,
		sugar:  l.sugar,
		fields: newFields,
	}
}

// WithFields 添加多个字段
func (l *Logger) WithFields(fields map[string]interface{}) *Logger {
	l.mutex.Lock()
	defer l.mutex.Unlock()

	newFields := make([]zap.Field, len(l.fields)+len(fields))
	copy(newFields, l.fields)

	i := len(l.fields)
	for key, value := range fields {
		newFields[i] = zap.Any(key, value)
		i++
	}

	return &Logger{
		Logger: l.Logger,
		sugar:  l.sugar,
		fields: newFields,
	}
}

// Debug 调试日志
func (l *Logger) Debug(msg string, fields ...zap.Field) {
	allFields := append(l.fields, fields...)
	l.Logger.Debug(msg, allFields...)
}

// Info 信息日志
func (l *Logger) Info(msg string, fields ...zap.Field) {
	allFields := append(l.fields, fields...)
	l.Logger.Info(msg, allFields...)
}

// Warn 警告日志
func (l *Logger) Warn(msg string, fields ...zap.Field) {
	allFields := append(l.fields, fields...)
	l.Logger.Warn(msg, allFields...)
}

// Error 错误日志
func (l *Logger) Error(msg string, fields ...zap.Field) {
	allFields := append(l.fields, fields...)
	l.Logger.Error(msg, allFields...)
}

// Fatal 致命错误日志
func (l *Logger) Fatal(msg string, fields ...zap.Field) {
	allFields := append(l.fields, fields...)
	l.Logger.Fatal(msg, allFields...)
}

// Panic 恐慌日志
func (l *Logger) Panic(msg string, fields ...zap.Field) {
	allFields := append(l.fields, fields...)
	l.Logger.Panic(msg, allFields...)
}

// Debugf 格式化调试日志
func (l *Logger) Debugf(template string, args ...interface{}) {
	l.sugar.Debugf(template, args...)
}

// Infof 格式化信息日志
func (l *Logger) Infof(template string, args ...interface{}) {
	l.sugar.Infof(template, args...)
}

// Warnf 格式化警告日志
func (l *Logger) Warnf(template string, args ...interface{}) {
	l.sugar.Warnf(template, args...)
}

// Errorf 格式化错误日志
func (l *Logger) Errorf(template string, args ...interface{}) {
	l.sugar.Errorf(template, args...)
}

// Fatalf 格式化致命错误日志
func (l *Logger) Fatalf(template string, args ...interface{}) {
	l.sugar.Fatalf(template, args...)
}

// Panicf 格式化恐慌日志
func (l *Logger) Panicf(template string, args ...interface{}) {
	l.sugar.Panicf(template, args...)
}

// Sync 同步日志缓冲区
func (l *Logger) Sync() error {
	return l.Logger.Sync()
}

// InitGlobalLogger 初始化全局日志记录器
func InitGlobalLogger(config *LogConfig) {
	once.Do(func() {
		globalLogger = NewLogger(config)
	})
}

// GetGlobalLogger 获取全局日志记录器
func GetGlobalLogger() *Logger {
	if globalLogger == nil {
		// 使用默认配置初始化
		InitGlobalLogger(&LogConfig{
			Level:       "info",
			Format:      "console",
			Output:      "stdout",
			Development: true,
		})
	}
	return globalLogger
}

// 全局日志函数
func Debug(msg string, fields ...zap.Field) {
	GetGlobalLogger().Debug(msg, fields...)
}

func Debugf(template string, args ...interface{}) {
	GetGlobalLogger().Debugf(template, args...)
}

func Info(msg string, fields ...zap.Field) {
	GetGlobalLogger().Info(msg, fields...)
}

func Infof(template string, args ...interface{}) {
	GetGlobalLogger().Infof(template, args...)
}

func Warn(msg string, fields ...zap.Field) {
	GetGlobalLogger().Warn(msg, fields...)
}

func Warnf(template string, args ...interface{}) {
	GetGlobalLogger().Warnf(template, args...)
}

func Error(msg string, fields ...zap.Field) {
	GetGlobalLogger().Error(msg, fields...)
}

func Errorf(template string, args ...interface{}) {
	GetGlobalLogger().Errorf(template, args...)
}

func Fatal(msg string, fields ...zap.Field) {
	GetGlobalLogger().Fatal(msg, fields...)
}

func Fatalf(template string, args ...interface{}) {
	GetGlobalLogger().Fatalf(template, args...)
}

func Panic(msg string, fields ...zap.Field) {
	GetGlobalLogger().Panic(msg, fields...)
}

func Panicf(template string, args ...interface{}) {
	GetGlobalLogger().Panicf(template, args...)
}

func WithField(key string, value interface{}) *Logger {
	return GetGlobalLogger().WithField(key, value)
}

func WithFields(fields map[string]interface{}) *Logger {
	return GetGlobalLogger().WithFields(fields)
}

// Sync 同步全局日志缓冲区
func Sync() error {
	return GetGlobalLogger().Sync()
}

// PerformanceLogger 性能日志记录器
type PerformanceLogger struct {
	logger    *Logger
	startTime time.Time
	operation string
}

// NewPerformanceLogger 创建性能日志记录器
func NewPerformanceLogger(operation string) *PerformanceLogger {
	return &PerformanceLogger{
		logger:    GetGlobalLogger(),
		startTime: time.Now(),
		operation: operation,
	}
}

// End 结束性能测量
func (p *PerformanceLogger) End() {
	duration := time.Since(p.startTime)
	p.logger.Info("Performance measurement",
		zap.String("operation", p.operation),
		zap.Duration("duration", duration),
		zap.Int64("duration_ms", duration.Milliseconds()),
	)
}

// EndWithFields 带字段结束性能测量
func (p *PerformanceLogger) EndWithFields(fields map[string]interface{}) {
	duration := time.Since(p.startTime)

	zapFields := make([]zap.Field, 0, len(fields)+3)
	zapFields = append(zapFields,
		zap.String("operation", p.operation),
		zap.Duration("duration", duration),
		zap.Int64("duration_ms", duration.Milliseconds()),
	)

	for key, value := range fields {
		zapFields = append(zapFields, zap.Any(key, value))
	}

	p.logger.Info("Performance measurement", zapFields...)
}

// AccessLogger 访问日志记录器
type AccessLogger struct {
	logger *Logger
}

// NewAccessLogger 创建访问日志记录器
func NewAccessLogger() *AccessLogger {
	return &AccessLogger{
		logger: GetGlobalLogger(),
	}
}

// LogRequest 记录请求日志
func (al *AccessLogger) LogRequest(method, path, clientIP string, statusCode int, duration time.Duration, size int64) {
	al.logger.Info("HTTP Request",
		zap.String("method", method),
		zap.String("path", path),
		zap.String("client_ip", clientIP),
		zap.Int("status_code", statusCode),
		zap.Duration("duration", duration),
		zap.Int64("response_size", size),
	)
}

// GameLogger 游戏专用日志记录器
type GameLogger struct {
	logger *Logger
	gameID string
	roomID string
}

// NewGameLogger 创建游戏日志记录器
func NewGameLogger(gameID, roomID string) *GameLogger {
	return &GameLogger{
		logger: GetGlobalLogger().WithFields(map[string]interface{}{
			"game_id":   gameID,
			"room_id":   roomID,
			"component": "game",
		}),
		gameID: gameID,
		roomID: roomID,
	}
}

// LogPlayerAction 记录玩家操作
func (gl *GameLogger) LogPlayerAction(userID uint64, action string, data interface{}) {
	gl.logger.Info("Player action",
		zap.Uint64("user_id", userID),
		zap.String("action", action),
		zap.Any("data", data),
	)
}

// LogGameEvent 记录游戏事件
func (gl *GameLogger) LogGameEvent(eventType string, data interface{}) {
	gl.logger.Info("Game event",
		zap.String("event_type", eventType),
		zap.Any("data", data),
	)
}

// SecurityLogger 安全日志记录器
type SecurityLogger struct {
	logger *Logger
}

// NewSecurityLogger 创建安全日志记录器
func NewSecurityLogger() *SecurityLogger {
	return &SecurityLogger{
		logger: GetGlobalLogger().WithField("component", "security"),
	}
}

// LogSecurityEvent 记录安全事件
func (sl *SecurityLogger) LogSecurityEvent(eventType, clientIP string, userID uint64, details interface{}) {
	sl.logger.Warn("Security event",
		zap.String("event_type", eventType),
		zap.String("client_ip", clientIP),
		zap.Uint64("user_id", userID),
		zap.Any("details", details),
	)
}

// LogAuthAttempt 记录认证尝试
func (sl *SecurityLogger) LogAuthAttempt(username, clientIP string, success bool, reason string) {
	level := zapcore.InfoLevel
	if !success {
		level = zapcore.WarnLevel
	}

	sl.logger.Log(level, "Authentication attempt",
		zap.String("username", username),
		zap.String("client_ip", clientIP),
		zap.Bool("success", success),
		zap.String("reason", reason),
	)
}

// BusinessLogger 业务日志记录器
type BusinessLogger struct {
	logger *Logger
}

// NewBusinessLogger 创建业务日志记录器
func NewBusinessLogger() *BusinessLogger {
	return &BusinessLogger{
		logger: GetGlobalLogger().WithField("component", "business"),
	}
}

// LogUserLogin 记录用户登录
func (bl *BusinessLogger) LogUserLogin(userID uint64, username, platform, clientIP string) {
	bl.logger.Info("User login",
		zap.Uint64("user_id", userID),
		zap.String("username", username),
		zap.String("platform", platform),
		zap.String("client_ip", clientIP),
	)
}

// LogUserLogout 记录用户登出
func (bl *BusinessLogger) LogUserLogout(userID uint64, onlineTime time.Duration) {
	bl.logger.Info("User logout",
		zap.Uint64("user_id", userID),
		zap.Duration("online_time", onlineTime),
	)
}

// LogGameStart 记录游戏开始
func (bl *BusinessLogger) LogGameStart(roomID uint64, gameType string, players []uint64) {
	bl.logger.Info("Game started",
		zap.Uint64("room_id", roomID),
		zap.String("game_type", gameType),
		zap.Uint64s("players", players),
	)
}

// LogGameEnd 记录游戏结束
func (bl *BusinessLogger) LogGameEnd(roomID uint64, winner uint64, duration time.Duration, reason string) {
	bl.logger.Info("Game ended",
		zap.Uint64("room_id", roomID),
		zap.Uint64("winner", winner),
		zap.Duration("duration", duration),
		zap.String("reason", reason),
	)
}

// StructuredLogger 结构化日志记录器
type StructuredLogger struct {
	logger  *Logger
	context map[string]interface{}
	mutex   sync.RWMutex
}

// NewStructuredLogger 创建结构化日志记录器
func NewStructuredLogger(context map[string]interface{}) *StructuredLogger {
	return &StructuredLogger{
		logger:  GetGlobalLogger(),
		context: context,
	}
}

// SetContext 设置上下文
func (sl *StructuredLogger) SetContext(key string, value interface{}) {
	sl.mutex.Lock()
	defer sl.mutex.Unlock()

	if sl.context == nil {
		sl.context = make(map[string]interface{})
	}
	sl.context[key] = value
}

// LogWithContext 带上下文记录日志
func (sl *StructuredLogger) LogWithContext(level zapcore.Level, msg string, additionalFields map[string]interface{}) {
	sl.mutex.RLock()
	defer sl.mutex.RUnlock()

	fields := make([]zap.Field, 0, len(sl.context)+len(additionalFields))

	// 添加上下文字段
	for key, value := range sl.context {
		fields = append(fields, zap.Any(key, value))
	}

	// 添加额外字段
	for key, value := range additionalFields {
		fields = append(fields, zap.Any(key, value))
	}

	sl.logger.Log(level, msg, fields...)
}

// MetricsLogger 指标日志记录器
type MetricsLogger struct {
	logger *Logger
}

// NewMetricsLogger 创建指标日志记录器
func NewMetricsLogger() *MetricsLogger {
	return &MetricsLogger{
		logger: GetGlobalLogger().WithField("component", "metrics"),
	}
}

// LogMetric 记录指标
func (ml *MetricsLogger) LogMetric(name string, value float64, labels map[string]string) {
	fields := []zap.Field{
		zap.String("metric_name", name),
		zap.Float64("value", value),
	}

	for key, val := range labels {
		fields = append(fields, zap.String(fmt.Sprintf("label_%s", key), val))
	}

	ml.logger.Info("Metric recorded", fields...)
}

// AuditLogger 审计日志记录器
type AuditLogger struct {
	logger *Logger
}

// NewAuditLogger 创建审计日志记录器
func NewAuditLogger() *AuditLogger {
	return &AuditLogger{
		logger: GetGlobalLogger().WithField("component", "audit"),
	}
}

// LogAuditEvent 记录审计事件
func (al *AuditLogger) LogAuditEvent(userID uint64, action, resource string, success bool, details interface{}) {
	al.logger.Info("Audit event",
		zap.Uint64("user_id", userID),
		zap.String("action", action),
		zap.String("resource", resource),
		zap.Bool("success", success),
		zap.Any("details", details),
		zap.Time("audit_time", time.Now()),
	)
}

// Close 关闭日志器并刷新缓冲区
func Close() error {
	if globalLogger != nil {
		return globalLogger.Sync()
	}
	return nil
}

// SetGlobalLevel 设置全局日志级别
func SetGlobalLevel(level string) {
	// 这需要重新创建logger，zap不支持动态调整级别
	// 在生产环境中，建议使用配置文件或环境变量
}

// IsDebugEnabled 检查是否启用调试级别
func IsDebugEnabled() bool {
	return GetGlobalLogger().Core().Enabled(zapcore.DebugLevel)
}
