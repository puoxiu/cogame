package mongodb

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/puoxiu/cogame/internal/logger"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"
	"go.mongodb.org/mongo-driver/mongo/writeconcern"
)

// MongoConfig MongoDB配置
type MongoConfig struct {
	// 单机模式
	URI      string `mapstructure:"uri"`
	Database string `mapstructure:"database"`

	// 副本集模式
	ReplicaSet     bool     `mapstructure:"replica_set"`
	ReplicaSetName string   `mapstructure:"replica_set_name"`
	Hosts          []string `mapstructure:"hosts"`
	AuthSource     string   `mapstructure:"auth_source"`
	Username       string   `mapstructure:"username"`
	Password       string   `mapstructure:"password"`

	// 分片模式
	ShardedCluster bool     `mapstructure:"sharded_cluster"`
	MongosHosts    []string `mapstructure:"mongos_hosts"`

	// 连接配置
	ConnectTimeout  time.Duration `mapstructure:"connect_timeout"`
	MaxPoolSize     uint64        `mapstructure:"max_pool_size"`
	MinPoolSize     uint64        `mapstructure:"min_pool_size"`
	MaxConnIdleTime time.Duration `mapstructure:"max_conn_idle_time"`

	// 读写配置
	ReadPreference string `mapstructure:"read_preference"` // primary, primaryPreferred, secondary, etc.
	WriteConcern   string `mapstructure:"write_concern"`   // majority, 1, 2, etc.
	ReadConcern    string `mapstructure:"read_concern"`    // local, available, majority, etc.

	// SSL/TLS配置
	TLSEnabled  bool   `mapstructure:"tls_enabled"`
	TLSCertFile string `mapstructure:"tls_cert_file"`
	TLSKeyFile  string `mapstructure:"tls_key_file"`
	TLSCAFile   string `mapstructure:"tls_ca_file"`
}

// MongoManager MongoDB管理器
type MongoManager struct {
	client   *mongo.Client
	database *mongo.Database
	config   *MongoConfig
	ctx      context.Context
	mode     string // "single", "replica_set", "sharded"
}

// NewMongoManager 创建MongoDB管理器
func NewMongoManager(config *MongoConfig) (*MongoManager, error) {
	fmt.Println("======: ", config)
	ctx := context.Background()

	manager := &MongoManager{
		config: config,
		ctx:    ctx,
	}

	var clientOptions *options.ClientOptions
	var err error

	// 根据配置选择MongoDB模式
	if config.ShardedCluster {
		manager.mode = "sharded"
		clientOptions, err = manager.buildShardedClusterOptions()
	} else if config.ReplicaSet {
		manager.mode = "replica_set"
		clientOptions, err = manager.buildReplicaSetOptions()
	} else {
		manager.mode = "single"
		clientOptions, err = manager.buildSingleOptions()
	}

	if err != nil {
		return nil, fmt.Errorf("failed to build client options: %v", err)
	}

	// 连接MongoDB
	client, err := mongo.Connect(ctx, clientOptions)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to mongodb: %v", err)
	}

	// 测试连接
	if err := client.Ping(ctx, nil); err != nil {
		client.Disconnect(ctx)
		return nil, fmt.Errorf("failed to ping mongodb: %v", err)
	}

	manager.client = client
	manager.database = client.Database(config.Database)

	logger.Infof("MongoDB connected in %s mode", manager.mode)
	return manager, nil
}

// buildSingleOptions 构建单机模式选项
func (mm *MongoManager) buildSingleOptions() (*options.ClientOptions, error) {
	opts := options.Client().
		ApplyURI(mm.config.URI).
		SetConnectTimeout(mm.config.ConnectTimeout).
		SetMaxPoolSize(mm.config.MaxPoolSize).
		SetMinPoolSize(mm.config.MinPoolSize).
		SetMaxConnIdleTime(mm.config.MaxConnIdleTime)

	// 添加认证信息
	if mm.config.Username != "" && mm.config.Password != "" {
		credential := options.Credential{
			Username:   mm.config.Username,
			Password:   mm.config.Password,
			AuthSource: mm.config.AuthSource,
		}
		opts.SetAuth(credential)
	}

	return opts, nil
}

// buildReplicaSetOptions 构建副本集模式选项
func (mm *MongoManager) buildReplicaSetOptions() (*options.ClientOptions, error) {
	if len(mm.config.Hosts) == 0 {
		return nil, fmt.Errorf("replica set hosts not configured")
	}

	if mm.config.ReplicaSetName == "" {
		return nil, fmt.Errorf("replica set name not configured")
	}

	// 构建连接URI
	uri := "mongodb://"
	if mm.config.Username != "" && mm.config.Password != "" {
		uri += fmt.Sprintf("%s:%s@", mm.config.Username, mm.config.Password)
	}
	uri += strings.Join(mm.config.Hosts, ",")
	uri += fmt.Sprintf("/%s?replicaSet=%s", mm.config.Database, mm.config.ReplicaSetName)

	if mm.config.AuthSource != "" {
		uri += fmt.Sprintf("&authSource=%s", mm.config.AuthSource)
	}

	opts := options.Client().
		ApplyURI(uri).
		SetConnectTimeout(mm.config.ConnectTimeout).
		SetMaxPoolSize(mm.config.MaxPoolSize).
		SetMinPoolSize(mm.config.MinPoolSize).
		SetMaxConnIdleTime(mm.config.MaxConnIdleTime).
		SetReplicaSet(mm.config.ReplicaSetName)

	// 设置读偏好
	if mm.config.ReadPreference != "" {
		readPref, err := parseReadPreference(mm.config.ReadPreference)
		if err != nil {
			return nil, fmt.Errorf("invalid read preference: %v", err)
		}
		opts.SetReadPreference(readPref)
	}

	// 设置写关注
	if mm.config.WriteConcern != "" {
		writeConcern, err := parseWriteConcern(mm.config.WriteConcern)
		if err != nil {
			return nil, fmt.Errorf("invalid write concern: %v", err)
		}
		opts.SetWriteConcern(writeConcern)
	}

	return opts, nil
}

// buildShardedClusterOptions 构建分片集群模式选项
func (mm *MongoManager) buildShardedClusterOptions() (*options.ClientOptions, error) {
	if len(mm.config.MongosHosts) == 0 {
		return nil, fmt.Errorf("mongos hosts not configured")
	}

	// 构建连接URI
	uri := "mongodb://"
	if mm.config.Username != "" && mm.config.Password != "" {
		uri += fmt.Sprintf("%s:%s@", mm.config.Username, mm.config.Password)
	}
	uri += strings.Join(mm.config.MongosHosts, ",")
	uri += fmt.Sprintf("/%s", mm.config.Database)

	if mm.config.AuthSource != "" {
		uri += fmt.Sprintf("?authSource=%s", mm.config.AuthSource)
	}

	opts := options.Client().
		ApplyURI(uri).
		SetConnectTimeout(mm.config.ConnectTimeout).
		SetMaxPoolSize(mm.config.MaxPoolSize).
		SetMinPoolSize(mm.config.MinPoolSize).
		SetMaxConnIdleTime(mm.config.MaxConnIdleTime)

	return opts, nil
}

// parseReadPreference 解析读偏好
func parseReadPreference(pref string) (*readpref.ReadPref, error) {
	switch pref {
	case "primary":
		return readpref.Primary(), nil
	case "primaryPreferred":
		return readpref.PrimaryPreferred(), nil
	case "secondary":
		return readpref.Secondary(), nil
	case "secondaryPreferred":
		return readpref.SecondaryPreferred(), nil
	case "nearest":
		return readpref.Nearest(), nil
	default:
		return nil, fmt.Errorf("unknown read preference: %s", pref)
	}
}

// parseWriteConcern 解析写关注
func parseWriteConcern(concern string) (*writeconcern.WriteConcern, error) {
	switch concern {
	case "majority":
		return writeconcern.New(writeconcern.WMajority()), nil
	case "1":
		return writeconcern.New(writeconcern.W(1)), nil
	case "2":
		return writeconcern.New(writeconcern.W(2)), nil
	case "3":
		return writeconcern.New(writeconcern.W(3)), nil
	default:
		// 尝试解析为数字
		if w := parseIntOrDefault(concern, -1); w > 0 {
			return writeconcern.New(writeconcern.W(w)), nil
		}
		return nil, fmt.Errorf("unknown write concern: %s", concern)
	}
}

// parseIntOrDefault 解析整数或返回默认值
func parseIntOrDefault(s string, defaultValue int) int {
	if val, err := strconv.Atoi(s); err == nil {
		return val
	}
	return defaultValue
}

// GetDatabase 获取数据库
func (mm *MongoManager) GetDatabase() *mongo.Database {
	return mm.database
}

// GetCollection 获取集合
func (mm *MongoManager) GetCollection(name string) *mongo.Collection {
	return mm.database.Collection(name)
}

// Close 关闭MongoDB连接
func (mm *MongoManager) Close() error {
	return mm.client.Disconnect(mm.ctx)
}
