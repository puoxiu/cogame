package pool

import "sync"

// ByteBufferPool 字节缓冲区池（管理不同大小的[]byte）
type ByteBufferPool struct {
	pools map[int]*sync.Pool // 按大小分组的子池（key=缓冲区大小，value=对应sync.Pool）
	sizes []int              // 支持的缓冲区大小列表（从小到大排序，如[64, 256, 1024]）
}

// NewByteBufferPool 创建字节缓冲区池
// 作用：初始化支持的缓冲区大小，并为每个大小创建sync.Pool
func NewByteBufferPool() *ByteBufferPool {
	// 定义支持的缓冲区大小（根据常见场景选择，可调整）
	sizes := []int{64, 256, 1024, 4096, 16384, 65536} // 64B、256B、1KB、4KB、16KB、64KB
	pools := make(map[int]*sync.Pool)

	// 为每个大小创建sync.Pool（sync.Pool是Go标准库的轻量对象池，适合临时对象）
	for _, size := range sizes {
		size := size // 闭包中捕获循环变量，避免变量共享
		pools[size] = &sync.Pool{
			// 工厂函数：创建指定大小的字节切片
			New: func() interface{} {
				return make([]byte, size)
			},
		}
	}

	return &ByteBufferPool{
		pools: pools,
		sizes: sizes,
	}
}

// GetBuffer 获取指定大小的字节缓冲区
// 逻辑：找到大于等于目标大小的最小缓冲区，若没有则直接创建
func (p *ByteBufferPool) GetBuffer(size int) []byte {
	// 遍历支持的大小，找到第一个大于等于目标size的缓冲区
	for _, poolSize := range p.sizes {
		if size <= poolSize {
			// 从对应子池获取缓冲区，并截取到需要的大小
			buf := p.pools[poolSize].Get().([]byte)
			return buf[:size]
		}
	}

	// 若目标大小超过最大支持的缓冲区，直接创建（不放入池，避免大对象占用内存）
	return make([]byte, size)
}

// PutBuffer 归还字节缓冲区（仅归还支持的大小，其他直接丢弃）
func (p *ByteBufferPool) PutBuffer(buf []byte) {
	size := cap(buf) // 用容量判断缓冲区大小（而非长度，因为长度可能被截取）

	// 找到对应的子池，归还缓冲区
	for _, poolSize := range p.sizes {
		if size == poolSize {
			p.pools[poolSize].Put(buf)
			return
		}
	}

	// 不在支持的大小范围内，直接丢弃（让GC回收）
}