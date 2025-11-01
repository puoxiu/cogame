package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/nsqio/go-nsq"
)

const (
	nsqLookupdAddr = "127.0.0.1:4161" // nsqlookupd HTTP 地址
	nsqdAddr       = "127.0.0.1:4150" // nsqd TCP 地址 (producer 使用)
	topic          = "order_created"
)

// 不同 channel 名称
var channels = []string{
	"inventory",
	"payment",
	"customer_service",
}

func main() {
	// 模拟一个topic 多个channnel，并且每个channel 都有一个consumer
	// Foo1()

	// 模拟一个producer 发布消息到topic
	Foo2()

}

func Foo2() {
	cfg := nsq.NewConfig()
	producer, err := nsq.NewProducer(nsqdAddr, cfg)
	if err != nil {
		log.Fatalf("failed to create producer: %v", err)
	}
	defer producer.Stop()

	msg := fmt.Sprintf(`{"order_id": %d, "created_at": "%s"}`, 1, time.Now().Format("2006-01-02 15:04:05"))
	if err := producer.Publish(topic, []byte(msg)); err != nil {
		log.Fatalf("publish failed: %v", err)
	} else {
		log.Printf("published message to topic=%s: %s", topic, msg)
	}
}



func Foo1() {
	// 全局等待组与取消
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup

	// 启动三个消费者，每个订阅不同 channel
	for _, ch := range channels {
		wg.Add(1)
		go func(channelName string) {
			defer wg.Done()
			if err := runConsumer(ctx, topic, channelName); err != nil {
				log.Printf("consumer(%s) exited with error: %v", channelName, err)
			}
		}(ch)
	}

	// producer：发布 10 条消息，每条间隔 500ms
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := runProducer(10, 500*time.Millisecond); err != nil {
			log.Printf("producer error: %v", err)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	select {
	case s := <-sig:
		log.Printf("Received signal %v, shutting down...", s)
		cancel()
	case <-ctx.Done():
	}

	wg.Wait()
	log.Println("program exited")
}

// runProducer 发布 n 条消息，每条消息间隔 interval
func runProducer(n int, interval time.Duration) error {
	cfg := nsq.NewConfig()
	prod, err := nsq.NewProducer(nsqdAddr, cfg)
	if err != nil {
		return fmt.Errorf("failed create producer: %w", err)
	}
	defer prod.Stop()

	prod.SetLogger(nil, 0)

	for i := 1; i <= n; i++ {
		msg := fmt.Sprintf(`{"order_id": %d, "created_at": "%s"}`, i, time.Now().Format(time.RFC3339))
		if err := prod.Publish(topic, []byte(msg)); err != nil {
			log.Printf("publish error: %v", err)
		} else {
			log.Printf("published message #%d to topic=%s", i, topic)
		}
		time.Sleep(interval)
	}
	// 给消费者一点时间消费
	time.Sleep(1 * time.Second)
	return nil
}

// runConsumer 创建一个 Consumer 订阅 topic+channel
func runConsumer(parentCtx context.Context, topic, channel string) error {
	cfg := nsq.NewConfig()
	// 根据需要设置消费者并发数（MaxInFlight）
	cfg.MaxInFlight = 10

	consumer, err := nsq.NewConsumer(topic, channel, cfg)
	if err != nil {
		return fmt.Errorf("nsq.NewConsumer error: %w", err)
	}

	// 每个 channel 使用不同的处理逻辑（这里只是打印并模拟不同耗时）
	handler := func(message *nsq.Message) error {
		switch channel {
		case "inventory":
			// 扣库存：快速操作
			log.Printf("[inventory][%s] process order: %s", channel, string(message.Body))
		case "payment":
			// 支付：可能耗时
			log.Printf("[payment][%s] processing payment for: %s", channel, string(message.Body))
			time.Sleep(200 * time.Millisecond)
		case "customer_service":
			// 客服：发送通知/异步延迟处理
			log.Printf("[customer_service][%s] notify cs for: %s", channel, string(message.Body))
			time.Sleep(50 * time.Millisecond)
		default:
			log.Printf("[%s] unknown channel body: %s", channel, string(message.Body))
		}
		return nil
	}

	consumer.AddHandler(nsq.HandlerFunc(handler))

	// 通过 nsqlookupd 自动发现 nsqd 节点并连接
	// if err := consumer.ConnectToNSQLookupd(nsqLookupdAddr); err != nil {
	// 	return fmt.Errorf("ConnectToNSQLookupd error: %w", err)
	// }
	// 单机部署，也可以直接连接 nsqd 地址
	if err := consumer.ConnectToNSQD(nsqdAddr); err != nil {
		return fmt.Errorf("ConnectToNSQD error: %w", err)
	}

	<-parentCtx.Done()

	// 优雅停止：先停止接收新消息，然后等待一段时间让当前消息处理完
	consumer.Stop()
	<-consumer.StopChan
	log.Printf("consumer channel=%s stopped", channel)
	return nil
}
