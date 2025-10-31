package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/puoxiu/cogame/internal/logger"
	"github.com/puoxiu/cogame/internal/server"
)

// go run cmd/main.go -config=config/config.yaml -node=login -id=login1



func main() {
	var (
		configFile = flag.String("config", "config/config.yaml", "配置文件路径")
		nodeType   = flag.String("node", "login", "节点类型")
		nodeID     = flag.String("id", "node1", "节点ID")
	)
	flag.Parse()
	
	if *configFile == "" || *nodeType == "" || *nodeID == "" {
		fmt.Println("使用方法: -config=config/config.yaml -node=login -id=node1")
		os.Exit(1)
	}

	// 启动对应服务
	srv := server.NewServer(*configFile, *nodeType, *nodeID)
	if err := srv.Start(); err != nil {
		logger.Fatal(fmt.Sprintf("Failed to start server: %v", err))
	}
	
}


