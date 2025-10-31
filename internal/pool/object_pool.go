package pool

import (
	"sync/atomic"
	"time"
)

// ObjectPool 通用对象池接口
type ObjectPool interface {
	// Get 获取一个对象（从池中复用，若无则创建新对象）
	Get() interface{}
	// Put 归还一个对象（重置后放入池中，供下次复用）
	Put(obj interface{})
	// Size 获取池中已创建的总对象数（包括在用和空闲）
	Size() int
	// Available 获取当前空闲可用的对象数
	Available() int
}

// GenericPool 通用对象池实现
// 作用：提供对象池的通用功能，通过工厂函数和重置函数适配不同类型的对象
type GenericPool struct {
	pool     chan interface{}   // 存储空闲对象的通道（线程安全，容量=maxSize）
	factory  func() interface{} // 对象工厂函数：用于创建新对象（当池中空闲对象不足时）
	reset    func(interface{})  // 对象重置函数：归还对象时调用，清除旧状态（如清空字段）
	maxSize  int                // 最大对象数（限制池的总大小，防止内存溢出）
	created  int64              // 已创建的对象总数（原子变量，线程安全计数）
	gotten   int64              // 累计获取对象的次数（统计用）
	put      int64              // 累计归还对象的次数（统计用）
}

// NewGenericPool 创建通用对象池
// 参数：
//   maxSize=最大对象数（池的容量上限）
//   factory=创建新对象的函数（如func() interface{} { return &Message{} }）
//   reset=归还对象时的重置函数（如func(obj interface{}) { obj.(*Message).Reset() }）
func NewGenericPool(maxSize int, factory func() interface{}, reset func(interface{})) *GenericPool {
	return &GenericPool{
		pool:    make(chan interface{}, maxSize), // 初始化带缓冲的channel，容量=maxSize
		factory: factory,
		reset:   reset,
		maxSize: maxSize,
	}
}

// Get 获取对象（线程安全）
// 逻辑：1. 先尝试从channel取空闲对象（非阻塞）；2. 若没有，且未达maxSize，创建新对象；3. 否则等待100ms，超时后仍创建新对象
func (p *GenericPool) Get() interface{} {
	// 累计获取次数累加（原子操作，线程安全）
	atomic.AddInt64(&p.gotten, 1)

	// 第一步：非阻塞尝试从池中取对象（若有空闲，直接返回）
	select {
	case obj := <-p.pool:
		return obj
	default:
		// 池中无空闲对象，进入下一步
	}

	// 第二步：检查已创建对象数是否小于maxSize（若未达上限，创建新对象）
	if int(atomic.LoadInt64(&p.created)) < p.maxSize {
		atomic.AddInt64(&p.created, 1)
		return p.factory()
	}

	// 第三步：已达maxSize，阻塞等待100ms（等待其他goroutine归还对象）等待期间有对象归还，复用;超时仍无对象，强制创建新对象（避免死等）
	select {
	case obj := <-p.pool: // 
		return obj
	case <-time.After(time.Millisecond * 100):
		return p.factory()
	}
}

// Put 归还对象（线程安全）
// 逻辑：1. 重置对象状态；2. 尝试放入channel（若池未满则存储，否则丢弃）
func (p *GenericPool) Put(obj interface{}) {
	if obj == nil {
		return
	}

	atomic.AddInt64(&p.put, 1)

	// 重置对象状态-如清空字段
	if p.reset != nil {
		p.reset(obj)
	}

	select {
	case p.pool <- obj:
	default:
		// 池已满，直接丢弃（避免阻塞当前goroutine）
	}
}

// Size 获取池中已创建的总对象数（包括在用和空闲）
func (p *GenericPool) Size() int {
	return int(atomic.LoadInt64(&p.created)) // 原子读取created计数
}

// Available 获取当前空闲可用的对象数（即池中待复用的对象数）
func (p *GenericPool) Available() int {
	return len(p.pool) // channel的长度即空闲对象数（channel是线程安全的，len操作安全）
}

// Stats 获取统计信息
func (p *GenericPool) Stats() (created, gotten, put int64) {
	// 原子读取统计字段（确保多goroutine下的数据一致性）
	return atomic.LoadInt64(&p.created), atomic.LoadInt64(&p.gotten), atomic.LoadInt64(&p.put)
}

