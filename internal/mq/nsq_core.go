package mq

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/nsqio/go-nsq"
	"github.com/puoxiu/cogame/internal/logger"
)

// NSQConfig NSQ配置
type NSQConfig struct {
	// 单节点模式
	NSQDAddress       string `mapstructure:"nsqd_address"`
	NSQLookupDAddress string `mapstructure:"nsqlookupd_address"`
	
	// 连接配置
	MaxInFlight    int           `mapstructure:"max_in_flight"`
	DialTimeout    time.Duration `mapstructure:"dial_timeout"`
	ReadTimeout    time.Duration `mapstructure:"read_timeout"`
	WriteTimeout   time.Duration `mapstructure:"write_timeout"`
	MessageTimeout time.Duration `mapstructure:"message_timeout"`

	// 集群模式
	ClusterMode         bool     `mapstructure:"cluster_mode"`
	NSQDAddresses       []string `mapstructure:"nsqd_addresses"`
	NSQLookupDAddresses []string `mapstructure:"nsqlookupd_addresses"`

	// 集群配置
	LoadBalancing       bool          `mapstructure:"load_balancing"`        // 负载均衡
	FailoverEnabled     bool          `mapstructure:"failover_enabled"`      // 故障转移
	HealthCheckInterval time.Duration `mapstructure:"health_check_interval"` // 健康检查间隔
	ProducerPoolSize    int           `mapstructure:"producer_pool_size"`    // 生产者池大小
}

// MessageHandler 消息处理器接口
type MessageHandler interface {
	HandleMessage(topic, channel string, data []byte) error
}

// NSQManager NSQ管理器
type NSQManager struct {
	config          *NSQConfig
	producers       []*nsq.Producer // 支持多个生产者（集群模式）
	producer        *nsq.Producer   // 主生产者（兼容性）
	consumers       map[string]*nsq.Consumer
	handlers        map[string]MessageHandler
	mutex           sync.RWMutex
	ctx             context.Context
	cancel          context.CancelFunc
	mode            string // "single", "cluster"
	currentProducer int    // 当前使用的生产者索引（轮询）
}


// NewNSQManager 创建NSQ管理器
func NewNSQManager(config *NSQConfig) (*NSQManager, error) {
	ctx, cancel := context.WithCancel(context.Background())

	manager := &NSQManager{
		config:    config,
		consumers: make(map[string]*nsq.Consumer),
		handlers:  make(map[string]MessageHandler),
		ctx:       ctx,
		cancel:    cancel,
		producers: make([]*nsq.Producer, 0),
	}

	var err error
	if config.ClusterMode {
		manager.mode = "cluster"
		err = manager.initClusterMode()
	} else {
		manager.mode = "single"
		err = manager.initSingleMode()
	}

	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to initialize NSQ: %v", err)
	}

	logger.Infof("NSQ manager initialized in %s mode", manager.mode)
	return manager, nil
}


// initSingleMode 初始化单节点模式
func (nm *NSQManager) initSingleMode() error {
	producerConfig := nsq.NewConfig()
	producerConfig.DialTimeout = nm.config.DialTimeout
	producerConfig.ReadTimeout = nm.config.ReadTimeout
	producerConfig.WriteTimeout = nm.config.WriteTimeout

	producer, err := nsq.NewProducer(nm.config.NSQDAddress, producerConfig)
	if err != nil {
		return fmt.Errorf("failed to create NSQ producer: %v", err)
	}

	// 测试连接
	if err := producer.Ping(); err != nil {
		producer.Stop()
		return fmt.Errorf("failed to ping NSQ: %v", err)
	}

	nm.producer = producer
	nm.producers = append(nm.producers, producer)

	logger.Infof("NSQ single mode initialized: %s", nm.config.NSQDAddress)
	return nil
}


// initClusterMode 初始化集群模式
func (nm *NSQManager) initClusterMode() error {
	if len(nm.config.NSQDAddresses) == 0 {
		return fmt.Errorf("NSQD addresses not configured for cluster mode")
	}

	producerConfig := nsq.NewConfig()
	producerConfig.DialTimeout = nm.config.DialTimeout
	producerConfig.ReadTimeout = nm.config.ReadTimeout
	producerConfig.WriteTimeout = nm.config.WriteTimeout

	// 为每个NSQD节点创建生产者
	for _, addr := range nm.config.NSQDAddresses {
		producer, err := nsq.NewProducer(addr, producerConfig)
		if err != nil {
			// 清理已创建的生产者
			nm.closeProducers()
			return fmt.Errorf("failed to create NSQ producer for %s: %v", addr, err)
		}

		// 测试连接
		if err := producer.Ping(); err != nil {
			producer.Stop()
			// 如果不是故障转移模式，则失败
			if !nm.config.FailoverEnabled {
				nm.closeProducers()
				return fmt.Errorf("failed to ping NSQ %s: %v", addr, err)
			}
			logger.Warnf("NSQD %s unavailable, will retry later", addr)
			continue
		}

		nm.producers = append(nm.producers, producer)
		logger.Infof("Connected to NSQD: %s", addr)
	}

	if len(nm.producers) == 0 {
		return fmt.Errorf("no NSQD nodes available")
	}

	// 设置主生产者为第一个可用的
	nm.producer = nm.producers[0]

	logger.Infof("NSQ cluster mode initialized: %d producers", len(nm.producers))
	return nil
}

// closeProducers 关闭所有生产者
func (nm *NSQManager) closeProducers() {
	for _, producer := range nm.producers {
		producer.Stop()
	}
	nm.producers = nm.producers[:0]
}


// Publish 发布消息
func (nm *NSQManager) Publish(topic string, data []byte) error {
	if nm.mode == "cluster" && nm.config.LoadBalancing && len(nm.producers) > 1 {
		// 如果是集群模式且启用负载均衡且有多个生产者， 则负载均衡发布
		return nm.publishWithLoadBalancing(topic, data)
	}
	return nm.producer.Publish(topic, data)
}


// publishWithLoadBalancing 负载均衡发布消息
func (nm *NSQManager) publishWithLoadBalancing(topic string, data []byte) error {
	nm.mutex.Lock()
	// 轮询选择生产者
	producer := nm.producers[nm.currentProducer%len(nm.producers)]
	nm.currentProducer++
	nm.mutex.Unlock()

	err := producer.Publish(topic, data)

	// 如果当前生产者失败且启用了故障转移，尝试其他生产者
	if err != nil && nm.config.FailoverEnabled && len(nm.producers) > 1 {
		for i, fallbackProducer := range nm.producers {
			if fallbackProducer == producer {
				continue // 跳过失败的生产者
			}

			if fallbackErr := fallbackProducer.Publish(topic, data); fallbackErr == nil {
				logger.Warnf("Failover successful: switched from producer %d to %d", nm.currentProducer-1, i)
				return nil
			}
		}
		return fmt.Errorf("all NSQ producers failed: %v", err)
	}

	return err
}


// GetClusterStats 获取集群统计信息
func (nm *NSQManager) GetClusterStats() map[string]interface{} {
	stats := map[string]interface{}{
		"mode":      nm.mode,
		"producers": len(nm.producers),
		"consumers": len(nm.consumers),
	}

	if nm.mode == "cluster" {
		stats["nsqd_addresses"] = nm.config.NSQDAddresses
		stats["load_balancing"] = nm.config.LoadBalancing
		stats["failover_enabled"] = nm.config.FailoverEnabled

		// 生产者健康状态
		producerStatus := make([]map[string]interface{}, len(nm.producers))
		for i, producer := range nm.producers {
			status := map[string]interface{}{
				"index": i,
			}

			// 尝试ping检查健康状态
			if err := producer.Ping(); err == nil {
				status["healthy"] = true
			} else {
				status["healthy"] = false
				status["error"] = err.Error()
			}

			producerStatus[i] = status
		}
		stats["producer_status"] = producerStatus
	}

	return stats
}

// PublishJSON 发布JSON消息
func (nm *NSQManager) PublishJSON(topic string, data interface{}) error {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal JSON: %v", err)
	}
	return nm.Publish(topic, jsonData)
}

// DeferredPublish 延迟发布消息
func (nm *NSQManager) DeferredPublish(topic string, delay time.Duration, data []byte) error {
	return nm.producer.DeferredPublish(topic, delay, data)
}

// Subscribe 订阅主题
func (nm *NSQManager) Subscribe(topic, channel string, handler MessageHandler) error {
	nm.mutex.Lock()
	defer nm.mutex.Unlock()

	key := fmt.Sprintf("%s_%s", topic, channel)
	if _, exists := nm.consumers[key]; exists {
		return fmt.Errorf("already subscribed to %s/%s", topic, channel)
	}

	config := nsq.NewConfig()
	config.MaxInFlight = nm.config.MaxInFlight
	config.MsgTimeout = nm.config.MessageTimeout

	consumer, err := nsq.NewConsumer(topic, channel, config)
	if err != nil {
		return fmt.Errorf("failed to create consumer: %v", err)
	}

	// 设置消息处理器
	consumer.AddHandler(&messageHandlerWrapper{
		handler: handler,
		topic:   topic,
		channel: channel,
	})

	// 连接到NSQLookupd
	if nm.mode == "cluster" && len(nm.config.NSQLookupDAddresses) > 0 {
		// 集群模式：连接到所有NSQLookupd
		for _, addr := range nm.config.NSQLookupDAddresses {
			if err := consumer.ConnectToNSQLookupd(addr); err != nil {
				if !nm.config.FailoverEnabled {
					return fmt.Errorf("failed to connect to NSQLookupd %s: %v", addr, err)
				}
				logger.Warnf("Failed to connect to NSQLookupd %s: %v", addr, err)
			} else {
				logger.Infof("Connected to NSQLookupd: %s", addr)
			}
		}
	} else {
		// 单机模式：连接到单个NSQLookupd
		if err := consumer.ConnectToNSQLookupd(nm.config.NSQLookupDAddress); err != nil {
			return fmt.Errorf("failed to connect to NSQLookupd: %v", err)
		}
	}

	nm.consumers[key] = consumer
	nm.handlers[key] = handler

	logger.Infof("Subscribed to topic: %s, channel: %s", topic, channel)
	return nil
}


// Unsubscribe 取消订阅
func (nm *NSQManager) Unsubscribe(topic, channel string) error {
	nm.mutex.Lock()
	defer nm.mutex.Unlock()

	key := fmt.Sprintf("%s_%s", topic, channel)
	consumer, exists := nm.consumers[key]
	if !exists {
		return fmt.Errorf("not subscribed to %s/%s", topic, channel)
	}

	consumer.Stop()
	<-consumer.StopChan

	delete(nm.consumers, key)
	delete(nm.handlers, key)

	logger.Info(fmt.Sprintf("Unsubscribed from topic: %s, channel: %s", topic, channel))
	return nil
}

// Close 关闭NSQ管理器
func (nm *NSQManager) Close() error {
	nm.cancel()

	// 停止所有消费者
	nm.mutex.Lock()
	for key, consumer := range nm.consumers {
		consumer.Stop()
		<-consumer.StopChan
		logger.Debug(fmt.Sprintf("Stopped consumer: %s", key))
	}
	nm.mutex.Unlock()

	// 停止生产者
	nm.producer.Stop()

	logger.Info("NSQ manager closed")
	return nil
}

// messageHandlerWrapper NSQ消息处理器包装器
type messageHandlerWrapper struct {
	handler MessageHandler
	topic   string
	channel string
}

// HandleMessage 实现nsq.Handler接口
func (mhw *messageHandlerWrapper) HandleMessage(message *nsq.Message) error {
	start := time.Now()

	err := mhw.handler.HandleMessage(mhw.topic, mhw.channel, message.Body)

	duration := time.Since(start)
	logger.Debug(fmt.Sprintf("Handled message from %s/%s in %v", mhw.topic, mhw.channel, duration))

	return err
}