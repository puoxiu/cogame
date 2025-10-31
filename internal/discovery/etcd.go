package discovery

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"sync"
	"time"

	"github.com/puoxiu/cogame/internal/logger"
	clientv3 "go.etcd.io/etcd/client/v3"
)

// ServiceInfo 服务信息
type ServiceInfo struct {
	NodeID     string            `json:"node_id"`
	NodeType   string            `json:"node_type"`
	Address    string            `json:"address"`
	Port       int               `json:"port"`
	Load       int               `json:"load"`
	Status     string            `json:"status"`
	Metadata   map[string]string `json:"metadata"`
	UpdateTime int64             `json:"update_time"`
}

// ServiceRegistry 服务注册接口
type ServiceRegistry interface {
	Register(info *ServiceInfo) error
	Unregister(nodeID string) error
	UpdateLoad(nodeID string, load int) error
	GetServices(nodeType string) ([]*ServiceInfo, error)
	GetService(nodeID string) (*ServiceInfo, error)
	Watch(nodeType string, callback func([]*ServiceInfo)) error
	Close() error
}

// ETCDRegistry ETCD服务注册实现
type ETCDRegistry struct {
	client    *clientv3.Client
	leaseID   clientv3.LeaseID
	keepAlive <-chan *clientv3.LeaseKeepAliveResponse
	ctx       context.Context
	cancel    context.CancelFunc
	mutex     sync.RWMutex
	services  map[string]*ServiceInfo // 本地缓存
	watchers  map[string][]func([]*ServiceInfo)
	keyPrefix string
}

// ETCDConfig ETCD集群配置
type ETCDConfig struct {
	Endpoints            []string      `mapstructure:"endpoints"`
	DialTimeout          time.Duration `mapstructure:"dial_timeout"`
	Username             string        `mapstructure:"username"`
	Password             string        `mapstructure:"password"`
	TLSEnabled           bool          `mapstructure:"tls_enabled"`
	TLSCertFile          string        `mapstructure:"tls_cert_file"`
	TLSKeyFile           string        `mapstructure:"tls_key_file"`
	TLSCAFile            string        `mapstructure:"tls_ca_file"`
	TLSInsecure          bool          `mapstructure:"tls_insecure"`
	AutoSyncInterval     time.Duration `mapstructure:"auto_sync_interval"`
	DialKeepAliveTime    time.Duration `mapstructure:"dial_keep_alive_time"`
	DialKeepAliveTimeout time.Duration `mapstructure:"dial_keep_alive_timeout"`
	MaxCallSendMsgSize   int           `mapstructure:"max_call_send_msg_size"`
	MaxCallRecvMsgSize   int           `mapstructure:"max_call_recv_msg_size"`
}

// NewETCDRegistry 创建ETCD服务注册器
func NewETCDRegistry(config *ETCDConfig) (*ETCDRegistry, error) {
	clientConfig := clientv3.Config{
		Endpoints:            config.Endpoints,
		DialTimeout:          config.DialTimeout,
		AutoSyncInterval:     config.AutoSyncInterval,
		DialKeepAliveTime:    config.DialKeepAliveTime,
		DialKeepAliveTimeout: config.DialKeepAliveTimeout,
		MaxCallSendMsgSize:   config.MaxCallSendMsgSize,
		MaxCallRecvMsgSize:   config.MaxCallRecvMsgSize,
	}

	// 设置认证
	if config.Username != "" && config.Password != "" {
		clientConfig.Username = config.Username
		clientConfig.Password = config.Password
	}

	// 设置TLS
	if config.TLSEnabled {
		tlsConfig, err := buildETCDTLSConfig(config)
		if err != nil {
			return nil, fmt.Errorf("failed to build TLS config: %v", err)
		}
		clientConfig.TLS = tlsConfig
	}

	client, err := clientv3.New(clientConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create etcd client: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	registry := &ETCDRegistry{
		client:    client,
		ctx:       ctx,
		cancel:    cancel,
		services:  make(map[string]*ServiceInfo),
		watchers:  make(map[string][]func([]*ServiceInfo)),
		keyPrefix: "/lufy/services/",
	}

	// 创建租约
	if err := registry.createLease(); err != nil {
		client.Close()
		cancel()
		return nil, err
	}

	// 启动租约续期
	go registry.keepAliveLoop()

	// 启动集群健康检查
	go registry.clusterHealthCheck()

	logger.Infof("ETCD service registry initialized with endpoints: %v", config.Endpoints)
	return registry, nil
}

// buildETCDTLSConfig 构建ETCD TLS配置
func buildETCDTLSConfig(config *ETCDConfig) (*tls.Config, error) {
	tlsConfig := &tls.Config{
		InsecureSkipVerify: config.TLSInsecure,
	}

	if config.TLSCertFile != "" && config.TLSKeyFile != "" {
		cert, err := tls.LoadX509KeyPair(config.TLSCertFile, config.TLSKeyFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load client cert: %v", err)
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
	}

	if config.TLSCAFile != "" {
		caCert, err := ioutil.ReadFile(config.TLSCAFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read CA cert: %v", err)
		}

		caCertPool := x509.NewCertPool()
		if !caCertPool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to parse CA cert")
		}
		tlsConfig.RootCAs = caCertPool
	}

	return tlsConfig, nil
}

// clusterHealthCheck 集群健康检查
func (r *ETCDRegistry) clusterHealthCheck() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			r.checkETCDClusterHealth()
		case <-r.ctx.Done():
			return
		}
	}
}

// checkETCDClusterHealth 检查ETCD集群健康状态
func (r *ETCDRegistry) checkETCDClusterHealth() {
	ctx, cancel := context.WithTimeout(r.ctx, 5*time.Second)
	defer cancel()

	// 检查集群成员状态
	memberList, err := r.client.MemberList(ctx)
	if err != nil {
		logger.Errorf("Failed to get ETCD member list: %v", err)
		return
	}

	healthyMembers := 0
	for _, member := range memberList.Members {
		// 检查成员健康状态
		if len(member.ClientURLs) > 0 {
			healthyMembers++
		}
	}

	logger.Debugf("ETCD cluster health: %d/%d members healthy",
		healthyMembers, len(memberList.Members))

	if healthyMembers < len(memberList.Members)/2+1 {
		logger.Warnf("ETCD cluster may lose quorum: %d/%d members healthy",
			healthyMembers, len(memberList.Members))
	}
}

// createLease 创建租约
func (r *ETCDRegistry) createLease() error {
	lease, err := r.client.Grant(r.ctx, 30) // 30秒TTL
	if err != nil {
		return fmt.Errorf("failed to create lease: %v", err)
	}

	r.leaseID = lease.ID

	// 保持租约活跃
	keepAlive, err := r.client.KeepAlive(r.ctx, r.leaseID)
	if err != nil {
		return fmt.Errorf("failed to keep lease alive: %v", err)
	}

	r.keepAlive = keepAlive
	return nil
}

// keepAliveLoop 租约续期循环
func (r *ETCDRegistry) keepAliveLoop() {
	for {
		select {
		case resp := <-r.keepAlive:
			if resp == nil {
				logger.Warn("Lease keep alive channel closed, recreating lease")
				if err := r.createLease(); err != nil {
					logger.Error(fmt.Sprintf("Failed to recreate lease: %v", err))
					time.Sleep(5 * time.Second)
					continue
				}
			}
		case <-r.ctx.Done():
			return
		}
	}
}

// Register 注册服务
func (r *ETCDRegistry) Register(info *ServiceInfo) error {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	info.UpdateTime = time.Now().Unix()
	key := r.keyPrefix + info.NodeType + "/" + info.NodeID

	data, err := json.Marshal(info)
	if err != nil {
		return fmt.Errorf("failed to marshal service info: %v", err)
	}

	// 使用租约注册服务
	_, err = r.client.Put(r.ctx, key, string(data), clientv3.WithLease(r.leaseID))
	if err != nil {
		return fmt.Errorf("failed to register service: %v", err)
	}

	r.services[info.NodeID] = info
	logger.Info(fmt.Sprintf("Service registered: %s/%s", info.NodeType, info.NodeID))

	return nil
}

// Unregister 注销服务
func (r *ETCDRegistry) Unregister(nodeID string) error {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	info, exists := r.services[nodeID]
	if !exists {
		return fmt.Errorf("service %s not found", nodeID)
	}

	key := r.keyPrefix + info.NodeType + "/" + nodeID
	_, err := r.client.Delete(r.ctx, key)
	if err != nil {
		return fmt.Errorf("failed to unregister service: %v", err)
	}

	delete(r.services, nodeID)
	logger.Info(fmt.Sprintf("Service unregistered: %s", nodeID))

	return nil
}

// UpdateLoad 更新服务负载
func (r *ETCDRegistry) UpdateLoad(nodeID string, load int) error {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	info, exists := r.services[nodeID]
	if !exists {
		return fmt.Errorf("service %s not found", nodeID)
	}

	info.Load = load
	info.UpdateTime = time.Now().Unix()

	return r.Register(info)
}

// GetServices 获取指定类型的所有服务
func (r *ETCDRegistry) GetServices(nodeType string) ([]*ServiceInfo, error) {
	prefix := r.keyPrefix + nodeType + "/"
	resp, err := r.client.Get(r.ctx, prefix, clientv3.WithPrefix())
	if err != nil {
		return nil, fmt.Errorf("failed to get services: %v", err)
	}

	var services []*ServiceInfo
	for _, kv := range resp.Kvs {
		var info ServiceInfo
		if err := json.Unmarshal(kv.Value, &info); err != nil {
			logger.Error(fmt.Sprintf("Failed to unmarshal service info: %v", err))
			continue
		}
		services = append(services, &info)
	}

	return services, nil
}

// GetService 获取指定服务
func (r *ETCDRegistry) GetService(nodeID string) (*ServiceInfo, error) {
	r.mutex.RLock()
	info, exists := r.services[nodeID]
	r.mutex.RUnlock()

	if exists {
		return info, nil
	}

	// 从ETCD查询
	resp, err := r.client.Get(r.ctx, r.keyPrefix, clientv3.WithPrefix())
	if err != nil {
		return nil, fmt.Errorf("failed to get service: %v", err)
	}

	for _, kv := range resp.Kvs {
		var info ServiceInfo
		if err := json.Unmarshal(kv.Value, &info); err != nil {
			continue
		}
		if info.NodeID == nodeID {
			return &info, nil
		}
	}

	return nil, fmt.Errorf("service %s not found", nodeID)
}

// Watch 监听服务变化
func (r *ETCDRegistry) Watch(nodeType string, callback func([]*ServiceInfo)) error {
	r.mutex.Lock()
	if r.watchers[nodeType] == nil {
		r.watchers[nodeType] = make([]func([]*ServiceInfo), 0)
	}
	r.watchers[nodeType] = append(r.watchers[nodeType], callback)
	r.mutex.Unlock()

	prefix := r.keyPrefix + nodeType + "/"
	go func() {
		watchChan := r.client.Watch(r.ctx, prefix, clientv3.WithPrefix())
		for {
			select {
			case resp := <-watchChan:
				if resp.Err() != nil {
					logger.Error(fmt.Sprintf("Watch error: %v", resp.Err()))
					continue
				}

				// 获取最新的服务列表
				services, err := r.GetServices(nodeType)
				if err != nil {
					logger.Error(fmt.Sprintf("Failed to get services: %v", err))
					continue
				}

				// 通知所有监听者
				r.mutex.RLock()
				callbacks := r.watchers[nodeType]
				r.mutex.RUnlock()

				for _, cb := range callbacks {
					go cb(services)
				}

			case <-r.ctx.Done():
				return
			}
		}
	}()

	return nil
}

// Close 关闭注册器
func (r *ETCDRegistry) Close() error {
	r.cancel()

	// 撤销租约
	if r.leaseID != 0 {
		_, err := r.client.Revoke(r.ctx, r.leaseID)
		if err != nil {
			logger.Error(fmt.Sprintf("Failed to revoke lease: %v", err))
		}
	}

	return r.client.Close()
}

// ServiceDiscovery 服务发现器
type ServiceDiscovery struct {
	registry     ServiceRegistry
	nodeType     string
	loadBalancer LoadBalancer
	serviceCache map[string][]*ServiceInfo
	cacheMutex   sync.RWMutex
	updateTicker *time.Ticker
}

// LoadBalancer 负载均衡器接口
type LoadBalancer interface {
	Select(services []*ServiceInfo) *ServiceInfo
}

// RoundRobinLoadBalancer 轮询负载均衡器
type RoundRobinLoadBalancer struct {
	counters map[string]int
	mutex    sync.Mutex
}

// NewRoundRobinLoadBalancer 创建轮询负载均衡器
func NewRoundRobinLoadBalancer() *RoundRobinLoadBalancer {
	return &RoundRobinLoadBalancer{
		counters: make(map[string]int),
	}
}

// Select 选择服务
func (lb *RoundRobinLoadBalancer) Select(services []*ServiceInfo) *ServiceInfo {
	if len(services) == 0 {
		return nil
	}

	lb.mutex.Lock()
	defer lb.mutex.Unlock()

	// 获取服务类型
	nodeType := services[0].NodeType
	counter := lb.counters[nodeType]

	selected := services[counter%len(services)]
	lb.counters[nodeType] = counter + 1

	return selected
}

// WeightedLoadBalancer 加权负载均衡器
type WeightedLoadBalancer struct{}

// NewWeightedLoadBalancer 创建加权负载均衡器
func NewWeightedLoadBalancer() *WeightedLoadBalancer {
	return &WeightedLoadBalancer{}
}

// Select 基于负载选择服务
func (lb *WeightedLoadBalancer) Select(services []*ServiceInfo) *ServiceInfo {
	if len(services) == 0 {
		return nil
	}

	// 选择负载最低的服务
	var selected *ServiceInfo
	minLoad := int(^uint(0) >> 1) // 最大int值

	for _, service := range services {
		if service.Status == "online" && service.Load < minLoad {
			minLoad = service.Load
			selected = service
		}
	}

	if selected == nil {
		// 如果没有在线服务，返回第一个
		return services[0]
	}

	return selected
}

// NewServiceDiscovery 创建服务发现器
func NewServiceDiscovery(registry ServiceRegistry, nodeType string, loadBalancer LoadBalancer) *ServiceDiscovery {
	if loadBalancer == nil {
		loadBalancer = NewRoundRobinLoadBalancer()
	}

	discovery := &ServiceDiscovery{
		registry:     registry,
		nodeType:     nodeType,
		loadBalancer: loadBalancer,
		serviceCache: make(map[string][]*ServiceInfo),
		updateTicker: time.NewTicker(30 * time.Second),
	}

	// 启动缓存更新
	go discovery.updateCacheLoop()

	// 监听服务变化
	registry.Watch(nodeType, func(services []*ServiceInfo) {
		discovery.cacheMutex.Lock()
		discovery.serviceCache[nodeType] = services
		discovery.cacheMutex.Unlock()

		logger.Debug(fmt.Sprintf("Service cache updated for %s: %d services", nodeType, len(services)))
	})

	return discovery
}

// GetService 获取服务实例
func (sd *ServiceDiscovery) GetService(nodeType string) *ServiceInfo {
	sd.cacheMutex.RLock()
	services, exists := sd.serviceCache[nodeType]
	sd.cacheMutex.RUnlock()

	if !exists || len(services) == 0 {
		// 从注册中心获取
		freshServices, err := sd.registry.GetServices(nodeType)
		if err != nil {
			logger.Error(fmt.Sprintf("Failed to get services from registry: %v", err))
			return nil
		}

		sd.cacheMutex.Lock()
		sd.serviceCache[nodeType] = freshServices
		services = freshServices
		sd.cacheMutex.Unlock()
	}

	return sd.loadBalancer.Select(services)
}

// GetAllServices 获取所有服务实例
func (sd *ServiceDiscovery) GetAllServices(nodeType string) []*ServiceInfo {
	sd.cacheMutex.RLock()
	services, exists := sd.serviceCache[nodeType]
	sd.cacheMutex.RUnlock()

	if !exists {
		freshServices, err := sd.registry.GetServices(nodeType)
		if err != nil {
			logger.Error(fmt.Sprintf("Failed to get services from registry: %v", err))
			return nil
		}

		sd.cacheMutex.Lock()
		sd.serviceCache[nodeType] = freshServices
		sd.cacheMutex.Unlock()

		return freshServices
	}

	return services
}

// updateCacheLoop 缓存更新循环
func (sd *ServiceDiscovery) updateCacheLoop() {
	for range sd.updateTicker.C {
		// 更新所有缓存的服务类型
		sd.cacheMutex.RLock()
		nodeTypes := make([]string, 0, len(sd.serviceCache))
		for nodeType := range sd.serviceCache {
			nodeTypes = append(nodeTypes, nodeType)
		}
		sd.cacheMutex.RUnlock()

		for _, nodeType := range nodeTypes {
			services, err := sd.registry.GetServices(nodeType)
			if err != nil {
				logger.Error(fmt.Sprintf("Failed to update cache for %s: %v", nodeType, err))
				continue
			}

			sd.cacheMutex.Lock()
			sd.serviceCache[nodeType] = services
			sd.cacheMutex.Unlock()
		}
	}
}

// Close 关闭服务发现器
func (sd *ServiceDiscovery) Close() error {
	sd.updateTicker.Stop()
	return sd.registry.Close()
}
