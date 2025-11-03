package monitoring

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/puoxiu/cogame/internal/logger"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/shirou/gopsutil/v3/process"
)

// AlertLevel 告警级别
type AlertLevel int

const (
	AlertLevelInfo    AlertLevel = iota // 0：信息（如服务启动）
	AlertLevelWarning                   // 1：警告（如 CPU 略高）
	AlertLevelError                     // 2：错误（如连接失败）
	AlertLevelCritical                  // 3：严重（如内存耗尽）
)


// -------------------------- 1. 总管理器：统筹所有监控组件 --------------------------
// MonitoringManager 监控模块总管理器，负责初始化、启动、停止所有监控功能
type MonitoringManager struct {
	registry   *prometheus.Registry   // Prometheus 指标注册器（管理所有指标）
	httpServer *http.Server           // HTTP 服务（提供 /health、/metrics 等接口）
	ginEngine  *gin.Engine            // Gin 框架实例（处理 HTTP 请求）
	alerts     *AlertManager          // 告警管理器（管理告警规则和通道）
	metrics    *MetricsCollector      // 指标收集器（定义和更新所有指标）
	ctx        context.Context        // 上下文（用于优雅关闭）
	cancel     context.CancelFunc     // 取消函数（停止定时任务和 HTTP 服务）
	nodeType   string                 // 节点类型
	nodeID     string                 // 节点 ID（区分集群中的不同节点）
}

// -------------------------- 2. 指标收集器：定义和管理所有监控指标 --------------------------
// MetricsCollector 指标收集器，封装所有系统/业务指标
type MetricsCollector struct {
	// 系统指标
	// Gauge: 可增可减，适合瞬时值 		Summary: 记录分布，适合耗时			Counter：只增不减，适合累计值
	cpuUsage    *prometheus.GaugeVec    // CPU 使用率（%）
	memoryUsage *prometheus.GaugeVec    // 内存使用量（字节）
	goroutines  *prometheus.GaugeVec    // Goroutine 数量
	heapSize    *prometheus.GaugeVec    // Go 堆内存大小（字节）
	heapObjects *prometheus.GaugeVec    // Go 堆内存对象数量
	gcDuration  *prometheus.SummaryVec  // Go GC 耗时（秒）

	// 业务指标
	connectionCount *prometheus.GaugeVec  // 活跃连接数（可增可减）
	actorCount      *prometheus.GaugeVec  // 活跃 Actor 数量（可增可减）
	messageCount    *prometheus.CounterVec // 处理消息总数（只增不减）
	errorCount      *prometheus.CounterVec // 错误总数（只增不减）
	requestDuration *prometheus.SummaryVec // 请求耗时（分布统计）
	dbConnections   *prometheus.GaugeVec  // 数据库连接数（可增可减）

	// 自定义指标--可扩展性
	customMetrics map[string]prometheus.Metric // 自定义指标映射
	mutex         sync.RWMutex                // 保护 customMetrics
}

// -------------------------- 3. 告警管理器：处理告警规则和发送 --------------------------
// AlertManager 告警管理器，负责添加告警规则、告警通道，定时检查告警条件
type AlertManager struct {
	rules    []AlertRule       // 所有告警规则（如 CPU>80% 触发告警）
	alertChannels []AlertChannel    // 所有告警通道（如日志、邮件、短信）
	history  []Alert           // 告警历史记录（方便查询）
	mutex    sync.RWMutex      // 保护规则/通道/历史的并发安全
}

// AlertRule 告警规则：定义“什么条件下触发告警”
type AlertRule struct {
	Name      string          // 规则名称（如 "high_cpu_usage"）
	Condition func() bool     // 告警条件（返回 true 则触发）
	Message   string          // 告警信息（如 "CPU 使用率超过 80%"）
	Level     AlertLevel      // 告警级别（信息/警告/错误/严重）
	Cooldown  time.Duration   // 冷却时间（避免告警风暴，如 5 分钟内只发一次）
	LastAlert time.Time       // 上次告警时间（用于计算冷却）
}

// AlertChannel 告警通道接口：定义“告警如何发送”
type AlertChannel interface {
	Send(alert Alert) error // 发送告警的方法（不同通道实现不同逻辑）
}

// Alert 告警实体：触发告警时的具体信息
type Alert struct {
	ID        string      // 告警 ID（唯一标识，如 "high_cpu_usage_1699999999"）
	Rule      string      // 关联规则名称
	Level     AlertLevel  // 告警级别
	Message   string      // 告警内容
	Timestamp time.Time   // 触发时间
	NodeID    string      // 节点 ID（哪个节点触发的告警）
	NodeType  string      // 节点类型（哪个类型的节点触发的告警）
}


func NewMetricsCollector(nodeType, nodeID string) *MetricsCollector {
	return &MetricsCollector{
		cpuUsage: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: fmt.Sprintf("%s_%s_%s_cpu_usage", nodeType, nodeID),
				Help: "Current CPU usage percentage",
			},
			[]string{ "node_type", "node_id"},
		),
		memoryUsage: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: fmt.Sprintf("%s_%s_%s_memory_usage", nodeType, nodeID),
				Help: "Current memory usage in bytes",
			},
			[]string{ "node_type", "node_id"},
		),
		goroutines: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: fmt.Sprintf("%s_%s_%s_goroutines", nodeType, nodeID),
				Help: "Current number of goroutines",
			},
			[]string{ "node_type", "node_id"},
		),
		heapSize: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: fmt.Sprintf("%s_%s_%s_heap_size_bytes", nodeType, nodeID),
				Help: "Current heap size in bytes",
			},
			[]string{ "node_type", "node_id"},
		),
		heapObjects: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: fmt.Sprintf("%s_%s_%s_heap_objects", nodeType, nodeID),
				Help: "Current number of heap objects",
			},
			[]string{ "node_type", "node_id"},
		),
		gcDuration: prometheus.NewSummaryVec(
			prometheus.SummaryOpts{
				Name: fmt.Sprintf("%s_%s_%s_gc_duration_seconds", nodeType, nodeID),
				Help: "Current GC duration in seconds",
			},
			[]string{ "node_type", "node_id"},
		),
		connectionCount: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: fmt.Sprintf("%s_%s_%s_connection_count", nodeType, nodeID),
				Help: "Current number of active connections",
			},
			[]string{ "node_type", "node_id"},
		),
		actorCount: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: fmt.Sprintf("%s_%s_%s_actor_count", nodeType, nodeID),
				Help: "Current number of active actors",
			},
			[]string{ "node_type", "node_id"},
		),
		messageCount: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: fmt.Sprintf("%s_%s_%s_message_count", nodeType, nodeID),
				Help: "Total number of messages processed",
			},
			[]string{ "node_type", "node_id"},
		),
		errorCount: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: fmt.Sprintf("%s_%s_%s_error_count", nodeType, nodeID),
				Help: "Total number of errors",
			},
			[]string{ "node_type", "node_id"},
		),
		requestDuration: prometheus.NewSummaryVec(
			prometheus.SummaryOpts{
				Name: fmt.Sprintf("%s_%s_%s_request_duration_seconds", nodeType, nodeID),
				Help: "Request duration in seconds",
			},
			[]string{ "node_type", "node_id"},
		),
		// 自定义指标（如业务相关指标）
		customMetrics: make(map[string]prometheus.Metric),
	}
}

// -------------------------- 适配 Prometheus Collector 接口 --------------------------
// 作用：让 registry 知道有哪些指标，返回指标的元信息（名称、帮助信息、标签）
func (mc *MetricsCollector) Describe(ch chan<- *prometheus.Desc) {
	// 遍历所有指标，调用其 Describe 方法，将描述写入 ch
	mc.cpuUsage.Describe(ch)
	mc.memoryUsage.Describe(ch)
	mc.goroutines.Describe(ch)
	mc.heapSize.Describe(ch)
	mc.heapObjects.Describe(ch)
	mc.gcDuration.Describe(ch)
	mc.connectionCount.Describe(ch)
	mc.actorCount.Describe(ch)
	mc.messageCount.Describe(ch)
	mc.errorCount.Describe(ch)
	mc.requestDuration.Describe(ch)
	mc.dbConnections.Describe(ch)
}

// Collect 向 Prometheus 提供指标当前值（必须实现）
// 作用：当 Prometheus 抓取 /metrics 时，会调用该方法获取最新指标值
func (mc *MetricsCollector) Collect(ch chan<- prometheus.Metric) {
	// 遍历所有指标，调用其 Collect 方法，将最新值写入 ch
	mc.cpuUsage.Collect(ch)
	mc.memoryUsage.Collect(ch)
	mc.goroutines.Collect(ch)
	mc.heapSize.Collect(ch)
	mc.heapObjects.Collect(ch)
	mc.gcDuration.Collect(ch)
	mc.connectionCount.Collect(ch)
	mc.actorCount.Collect(ch)
	mc.messageCount.Collect(ch)
	mc.errorCount.Collect(ch)
	mc.requestDuration.Collect(ch)
	mc.dbConnections.Collect(ch)

	// 收集自定义指标必须加读锁，避免并发写冲突
	mc.mutex.RLock()
	defer mc.mutex.RUnlock()
	for _, metric := range mc.customMetrics {
		ch <- metric
	}
}

// -------------------------- 指标更新逻辑 --------------------------
// collectMetrics 独立协程定时收集系统指标, 每隔 10 秒触发一次指标更新，保证指标时效性
func (mm *MonitoringManager) collectMetrics() {
	// 默认定时 10 秒执行一次
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			mm.updateSystemMetrics()
		case <-mm.ctx.Done(): // 收到关闭信号（如调用 Stop()），退出循环
			return
		}
	}
}
// updateSystemMetrics 更新系统指标的具体值（调用 gopsutil 和 runtime 获取数据）
func (mm *MonitoringManager) updateSystemMetrics() {
	// 1. 更新 CPU 使用率
	// cpu.Percent(0, false)：0 表示“立即返回当前使用率”，false 表示“不按 CPU 核心拆分，返回总使用率”
	if cpuPercent, err := cpu.Percent(0, false); err == nil && len(cpuPercent) > 0 {
		// WithLabelValues：设置标签值（node_type, node_id），Set：设置指标值
		mm.metrics.cpuUsage.WithLabelValues(mm.nodeType, mm.nodeID).Set(cpuPercent[0])
	} else {
		logger.Error(fmt.Sprintf("failed to get CPU usage: %v", err))
	}

	// 2. 更新内存使用量
	if memInfo, err := mem.VirtualMemory(); err == nil {
		mm.metrics.memoryUsage.WithLabelValues(mm.nodeType, mm.nodeID).Set(float64(memInfo.Used))
	} else {
		logger.Error(fmt.Sprintf("failed to get memory usage: %v", err))
	}

	// 3. 更新 Goroutine 数量
	mm.metrics.goroutines.WithLabelValues(mm.nodeType, mm.nodeID).Set(float64(runtime.NumGoroutine()))

	// 4. 更新 Go 堆内存信息
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)
	mm.metrics.heapSize.WithLabelValues(mm.nodeType, mm.nodeID).Set(float64(memStats.HeapSys))       // 堆内存总大小
	mm.metrics.heapObjects.WithLabelValues(mm.nodeType, mm.nodeID).Set(float64(memStats.HeapObjects)) // 堆内存对象数量

	// 5. 更新 GC 耗时（memStats.NumGC 是 GC 次数，PauseNs 是每次 GC 的耗时（纳秒））
	if memStats.NumGC > 0 {
		// (memStats.NumGC + 255) % 256：取最近一次 GC 的耗时（PauseNs 是环形缓冲区，存最近 256 次 GC 耗时）
		gcDurationNs := memStats.PauseNs[(memStats.NumGC+255)%256]
		mm.metrics.gcDuration.WithLabelValues(mm.nodeType, mm.nodeID).Observe(float64(gcDurationNs) / 1e9) // 转换为秒
	}
}

// -------------------------- 业务指标记录方法（供外部模块调用） --------------------------
// RecordMessage 记录消息处理数（如每次处理一条消息，调用该方法）
func (mm *MonitoringManager) RecordMessage() {
	mm.metrics.messageCount.WithLabelValues(mm.nodeType, mm.nodeID).Inc() // Inc()：计数器 +1
}

// RecordError 记录错误数（如每次发生错误，调用该方法）
func (mm *MonitoringManager) RecordError(errorType string) {
	mm.metrics.errorCount.WithLabelValues(mm.nodeType, mm.nodeID, errorType).Inc()
}

// SetConnectionCount 设置当前活跃连接数（如每次连接建立/关闭后更新）
func (mm *MonitoringManager) SetConnectionCount(count int) {
	mm.metrics.connectionCount.WithLabelValues(mm.nodeType, mm.nodeID).Set(float64(count))
}

// SetActorCount 设置当前活跃 Actor 数（如 Actor 启动/停止后更新）
func (mm *MonitoringManager) SetActorCount(count int) {
	mm.metrics.actorCount.WithLabelValues(mm.nodeType, mm.nodeID).Set(float64(count))
}

// SetDBConnections 设置当前数据库连接数（如数据库连接池更新后调用）
func (mm *MonitoringManager) SetDBConnections(count int) {
	mm.metrics.dbConnections.WithLabelValues(mm.nodeType, mm.nodeID).Set(float64(count))
}


// -------------------------- 告警通道实现：日志通道 --------------------------
// LogAlertChannel 日志告警通道：将告警信息输出到日志
// MailAlertChannel 邮件告警通道：将告警信息发送到指定邮箱--todo
// SmsAlertChannel 短信告警通道：将告警信息发送到指定手机号--todo

type LogAlertChannel struct{}

// Send 实现 AlertChannel 接口的 Send 方法：输出告警日志
func (lac *LogAlertChannel) Send(alert Alert) error {
	// 格式化告警信息，输出到日志（级别为 Warn）
	logger.Warn(fmt.Sprintf(
		"[ALERT] Level: %d, Rule: %s, Message: %s, Node: %s/%s, Time: %s",
		alert.Level,
		alert.Rule,
		alert.Message,
		alert.NodeType,
		alert.NodeID,
		alert.Timestamp.Format("2006-01-02 15:04:05"),
	))
	return nil
}

// -------------------------- 告警管理器核心方法 --------------------------
// NewAlertManager 创建告警管理器实例
func NewAlertManager() *AlertManager {
	return &AlertManager{
		rules:    make([]AlertRule, 0),  // 初始化空规则列表
		alertChannels: make([]AlertChannel, 0),// 初始化空告警通道列表
		history:  make([]Alert, 0),      // 初始化空告警历史
	}
}
// AddRule 向告警管理器添加一条告警规则（外部模块调用，如添加 CPU 高告警）
func (am *AlertManager) AddRule(rule AlertRule) {
	am.mutex.Lock()         // 加写锁，避免并发添加规则冲突
	defer am.mutex.Unlock() // 函数退出时解锁

	am.rules = append(am.rules, rule)
	logger.Info(fmt.Sprintf("added alert rule: %s", rule.Name))
}
// AddChannel 向告警管理器添加一个告警通道（外部模块调用，如添加日志通道）
func (am *AlertManager) AddChannel(channel AlertChannel) {
	am.mutex.Lock()
	defer am.mutex.Unlock()

	am.alertChannels = append(am.alertChannels, channel)
	logger.Info("added alert channel")
}
// GetRecentAlerts 获取最近的告警记录（供 /api/alerts 接口调用）
func (am *AlertManager) GetRecentAlerts(limit int) []Alert {
	am.mutex.RLock()         // 加读锁，读取历史时不阻塞其他读操作
	defer am.mutex.RUnlock()

	if len(am.history) <= limit {
		return am.history // 若历史数小于 limit，返回全部
	}
	return am.history[len(am.history)-limit:] // 否则返回最近的 limit 条
}

// -------------------------- 告警检查逻辑 --------------------------
// checkAlerts 定时检查所有告警规则（在 MonitoringManager 初始化时启动为协程）
func (mm *MonitoringManager) checkAlerts() {
	ticker := time.NewTicker(30 * time.Second) // 每隔 30 秒检查一次（可调整）
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			mm.alerts.CheckRules(mm.nodeID, mm.nodeType) // 调用 AlertManager 的检查方法
		case <-mm.ctx.Done():
			return
		}
	}
}
// CheckRules 检查所有告警规则，满足条件则发送告警（AlertManager 的核心方法）
func (am *AlertManager) CheckRules(nodeID, nodeType string) {
	am.mutex.Lock()
	defer am.mutex.Unlock()

	now := time.Now()

	// 遍历所有告警规则
	for i, rule := range am.rules {
		// 1. 检查冷却时间：若距离上次告警时间小于冷却时间，跳过
		if now.Sub(rule.LastAlert) < rule.Cooldown {
			continue
		}

		// 2. 检查告警条件：调用规则的 Condition 函数，返回 true 则触发告警
		if rule.Condition() {
			// 3. 构造告警实体
			alert := Alert{
				ID:        fmt.Sprintf("%s_%d", rule.Name, now.Unix()), // 用规则名+时间戳作为唯一 ID
				Rule:      rule.Name,
				Level:     rule.Level,
				Message:   rule.Message,
				Timestamp: now,
				NodeID:    nodeID,
				NodeType:  nodeType,
			}

			// 4. 向所有告警通道发送告警
			for _, channel := range am.alertChannels {
				if err := channel.Send(alert); err != nil {
					logger.Error(fmt.Sprintf("failed to send alert via channel: %v", err))
				}
			}

			// 5. 记录告警历史
			am.history = append(am.history, alert)

			// 6. 更新规则的上次告警时间（避免冷却期内再次触发）
			am.rules[i].LastAlert = now

			logger.Info(fmt.Sprintf("alert triggered: %s", rule.Name))
		}
	}
}

// -------------------------- HTTP 路由注册 --------------------------
// registerRoutes 注册监控模块的所有 HTTP 接口
func (mm *MonitoringManager) registerRoutes() {
	// 1. Prometheus 指标抓取接口（/metrics）
	// promhttp.HandlerFor：将 Prometheus registry 包装为 HTTP 处理器，供 Prometheus 服务器抓取指标
	mm.ginEngine.GET("/metrics", gin.WrapH(promhttp.HandlerFor(mm.registry, promhttp.HandlerOpts{})))

	// 2. Go pprof 性能分析接口（调试用，如查看 Goroutine 栈、内存分配）
	// gin.WrapF：将 http.HandlerFunc 包装为 Gin 处理器
	// http.DefaultServeMux 是 Go 标准库的默认路由，pprof 已注册到该路由
	mm.ginEngine.GET("/debug/pprof/", gin.WrapF(http.DefaultServeMux.ServeHTTP))
	mm.ginEngine.GET("/debug/pprof/cmdline", gin.WrapF(http.DefaultServeMux.ServeHTTP))
	mm.ginEngine.GET("/debug/pprof/profile", gin.WrapF(http.DefaultServeMux.ServeHTTP)) // CPU  profiling
	mm.ginEngine.GET("/debug/pprof/heap", gin.WrapF(http.DefaultServeMux.ServeHTTP))    // 堆内存 profiling
	mm.ginEngine.GET("/debug/pprof/goroutine", gin.WrapF(http.DefaultServeMux.ServeHTTP))// Goroutine 栈

	// 3. 健康检查接口（/health）：用于 Kubernetes、负载均衡器判断服务是否存活
	mm.ginEngine.GET("/health", mm.healthCheck)

	// 4. 自定义指标查询接口（/api/metrics）：返回格式化的指标数据（JSON 格式，方便前端展示）
	mm.ginEngine.GET("/api/metrics", mm.getMetrics)

	// 5. 告警历史查询接口（/api/alerts）：返回最近的告警记录
	mm.ginEngine.GET("/api/alerts", mm.getAlerts)

	// 6. 系统信息查询接口（/api/system）：返回节点、Go 运行时信息
	mm.ginEngine.GET("/api/system", mm.getSystemInfo)
}

// -------------------------- HTTP 接口 Handler 函数 --------------------------
// healthCheck 健康检查接口 Handler：返回服务状态（healthy/unhealthy）
func (mm *MonitoringManager) healthCheck(c *gin.Context) {
	// 可扩展：添加数据库连接检查、Redis 连接检查等，若失败则返回 status: "unhealthy"
	c.JSON(http.StatusOK, gin.H{
		"status":    "healthy",       // 服务状态
		"node_id":   mm.nodeID,       // 节点 ID
		"node_type": mm.nodeType,     // 节点类型
		"timestamp": time.Now().Unix(),// 时间戳（秒）
	})
}

// getMetrics 自定义指标查询接口 Handler：返回系统和 Go 运行时指标（JSON 格式）
func (mm *MonitoringManager) getMetrics(c *gin.Context) {
	// 1. 获取 Go 运行时内存信息
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	// 2. 获取系统 CPU、内存信息
	cpuPercent, _ := cpu.Percent(0, false) // 忽略错误，避免接口返回失败
	memInfo, _ := mem.VirtualMemory()

	// 3. 构造 JSON 响应
	metrics := map[string]interface{}{
		"system": map[string]interface{}{ // 系统指标
			"cpu_percent":    cpuPercent,          // CPU 使用率（%）
			"memory_used":    memInfo.Used,        // 内存使用量（字节）
			"memory_total":   memInfo.Total,       // 内存总量（字节）
			"memory_percent": memInfo.UsedPercent, // 内存使用率（%）
		},
		"runtime": map[string]interface{}{ // Go 运行时指标
			"goroutines":   runtime.NumGoroutine(), // Goroutine 数量
			"heap_alloc":   memStats.HeapAlloc,     // 已分配堆内存（字节）
			"heap_sys":     memStats.HeapSys,       // 堆内存总量（字节）
			"heap_objects": memStats.HeapObjects,   // 堆内存对象数量
			"gc_cycles":    memStats.NumGC,         // GC 总次数
		},
	}

	c.JSON(http.StatusOK, metrics)
}

// getAlerts 告警历史查询接口 Handler：返回最近的告警记录
func (mm *MonitoringManager) getAlerts(c *gin.Context) {
	// 从请求参数中获取 limit（默认 100 条）
	limit := c.DefaultQuery("limit", "100")
	limitInt := 100
	fmt.Sscanf(limit, "%d", &limitInt) // 转换为整数

	// 获取最近的告警记录
	alerts := mm.alerts.GetRecentAlerts(limitInt)

	c.JSON(http.StatusOK, gin.H{
		"alerts": alerts,  // 告警列表
		"count":  len(alerts), // 告警总数
	})
}

// getSystemInfo 系统信息接口 Handler：返回节点、进程、Go 环境信息
func (mm *MonitoringManager) getSystemInfo(c *gin.Context) {
	pid := int32(os.Getpid()) // 需导入 "os" 包
	proc, _ := process.NewProcess(pid)

	// 构造系统信息
	systemInfo := map[string]interface{}{
		"node_id":         mm.nodeID,                // 节点 ID
		"node_type":       mm.nodeType,              // 节点类型
		"go_version":      runtime.Version(),        // Go 版本（如 go1.21.0）
		"go_os":           runtime.GOOS,             // 操作系统（如 linux、darwin）
		"go_arch":         runtime.GOARCH,           // 架构（如 amd64、arm64）
		"process_id":      pid,                      // 当前进程 ID
		"process_start_time": time.Now().Unix(),     // 临时用当前时间，后续修正为进程启动时间
	}

	// 补充进程信息（如进程启动时间、命令行）
	if proc != nil {
		if createTime, err := proc.CreateTime(); err == nil {
			systemInfo["process_start_time"] = createTime / 1000 // 转换为秒（gopsutil 返回毫秒）
		}
		if cmdline, err := proc.Cmdline(); err == nil {
			systemInfo["cmdline"] = cmdline // 进程启动命令行（如 "./your-project -config config.yaml"）
		}
	}
	c.JSON(http.StatusOK, systemInfo)
}


// -------------------------- MonitoringManager 对外入口 --------------------------
// NewMonitoringManager 创建监控管理器实例（外部模块调用，初始化监控模块）
// 参数：nodeType（节点类型）、nodeID（节点 ID）、port（HTTP 服务端口）
func NewMonitoringManager(nodeType, nodeID string, port int) (*MonitoringManager, error) {
	registry := prometheus.NewRegistry()

	ctx, cancel := context.WithCancel(context.Background())

	gin.SetMode(gin.ReleaseMode)
	ginEngine := gin.New()
	ginEngine.Use(gin.Recovery()) // 注册恢复中间件，避免 panic 导致服务崩溃

	// 初始化指标收集器
	metricsCollector := NewMetricsCollector(nodeType, nodeID)

	// 将指标收集器注册到 Prometheus 注册器
	registry.MustRegister(metricsCollector) // MustRegister：注册失败会 panic（确保指标正确）

	// 初始化告警管理器
	alertManager := NewAlertManager()

	// 初始化 HTTP 服务（绑定地址和 Gin 引擎）
	httpServer := &http.Server{
		Addr:    fmt.Sprintf(":%d", port), // 监听地址（如 ":8080"）
		Handler: ginEngine,                // 用 Gin 处理 HTTP 请求
	}

	// 构造 MonitoringManager 实例
	manager := &MonitoringManager{
		registry:   registry,
		httpServer: httpServer,
		ginEngine:  ginEngine,
		alerts:     alertManager,
		metrics:    metricsCollector,
		ctx:        ctx,
		cancel:     cancel,
		nodeID:     nodeID,
		nodeType:   nodeType,
	}

	// 注册 HTTP 路由
	manager.registerRoutes()

	// 启动定时任务（指标收集、告警检查）：用协程启动，避免阻塞当前函数
	go manager.collectMetrics()
	go manager.checkAlerts()

	logger.Info(fmt.Sprintf("monitoring manager initialized successfully (port: %d, node: %s/%s)",
		port, nodeType, nodeID))

	return manager, nil
}

// Start 启动监控模块的 HTTP 服务（外部模块调用，启动监控接口）
func (mm *MonitoringManager) Start() error {
	// 启动 HTTP 服务：ListenAndServe 
	go func() {
		if err := mm.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			// 只有非 "关闭" 错误才日志报错（http.ErrServerClosed 是正常关闭的错误）
			logger.Error(fmt.Sprintf("monitoring HTTP server error: %v", err))
		}
	}()

	logger.Info(fmt.Sprintf("monitoring HTTP server started (address: %s)", mm.httpServer.Addr))
	return nil
}

// Stop 优雅停止监控模块（外部模块调用，如服务关闭前）
func (mm *MonitoringManager) Stop() error {
	// 1. 取消上下文：停止定时任务（collectMetrics、checkAlerts）
	mm.cancel()

	// 2. 优雅关闭 HTTP 服务：等待已有的请求处理完成（最多等 5 秒）
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := mm.httpServer.Shutdown(ctx); err != nil {
		return fmt.Errorf("failed to shutdown monitoring HTTP server: %v", err)
	}

	logger.Info("monitoring manager stopped successfully")
	return nil
}