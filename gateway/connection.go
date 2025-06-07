// Package gateway 实现了基于高性能网络 I/O 多路复用的 TCP 连接管理
// 本包中的 NIOConnection.go 文件主要实现以下功能：
// 1. 提供全局唯一的连接 ID 生成器
// 2. 实现高效的连接对象管理
// 3. 支持连接的生命周期管理
// 4. 提供连接相关的辅助功能
package gateway

import (
	"errors"
	"net"
	"sync"
	"time"
)

// 全局连接 ID 生成器实例
var node *ConnIDGenerater

// 连接 ID 生成器的相关常量配置
// 如果需要调整 ID 的编码格式，只需修改这些常量值
const (
	version      = uint64(0) // 版本控制
	sequenceBits = uint64(16)

	maxSequence = int64(-1) ^ (int64(-1) << sequenceBits)

	timeLeft    = uint8(16) // timeLeft = sequenceBits // 时间戳向左偏移量
	versionLeft = uint8(63) // 左移动到最高位
	// 2020-05-20 08:00:00 +0800 CST
	twepoch = int64(1589923200000) // 常量时间戳(毫秒)
)

// ConnIDGenerater 实现了一个基于雪花算法的连接 ID 生成器
// 使用时间戳和序列号组合生成唯一 ID，支持高并发场景
type ConnIDGenerater struct {
	mu        sync.Mutex
	LastStamp int64 // 记录上一次ID的时间戳
	Sequence  int64 // 当前毫秒已经生成的ID序列号(从0 开始累加) 1毫秒内最多生成2^16个ID
}

// NIOConnection 表示一个 TCP 连接对象
// 包含连接的唯一标识、文件描述符和底层 TCP 连接
type NIOConnection struct {
	id   uint64 // 进程级别的生命周期
	fd   int
	e    *epoller
	conn *net.TCPConn
}

func init() {
	node = &ConnIDGenerater{}
}

// getMilliSeconds 获取当前时间的毫秒时间戳
func (c *ConnIDGenerater) getMilliSeconds() int64 {
	return time.Now().UnixNano() / 1e6
}

// socketFD 获取 TCP 连接的底层文件描述符
// 通过 syscall.Conn 接口安全地获取文件描述符
func (c *NIOConnection) socketFD() int {
	syscallConn, err := c.conn.SyscallConn()
	if err != nil {
		panic(err)
	}

	var fd int
	err = syscallConn.Control(func(f uintptr) {
		fd = int(f)
	})
	if err != nil {
		panic(err)
	}
	return fd
}

// NewConnection 创建新的连接对象
// 为连接分配全局唯一的 ID，并获取其文件描述符
func NewConnection(conn *net.TCPConn) *NIOConnection {
	var id uint64
	var err error
	if id, err = node.NextID(); err != nil {
		panic(err) // 在线服务需要解决这个问题 ，报错而不能panic
	}
	return &NIOConnection{
		id:   id,
		fd:   (&NIOConnection{conn: conn}).socketFD(),
		conn: conn,
	}
}
// Close 关闭连接并清理相关资源
// 从全局表和 epoll 实例中移除连接对象
func (c *NIOConnection) Close() {
	ep.tables.Delete(c.id)
	if c.e != nil {
		c.e.fdToConnTable.Delete(c.fd)
	}
	err := c.conn.Close()
	panic(err)
}

// RemoteAddr 获取连接的远程地址
func (c *NIOConnection) RemoteAddr() string {
	return c.conn.RemoteAddr().String()
}

// BindEpoller 将连接绑定到指定的 epoll/iocp 实例
func (c *NIOConnection) BindEpoller(e *epoller) {
	c.e = e
}

// 这里的锁会自旋，不会多么影响性能，主要是临界区小
// NextID 生成下一个连接 ID
// 使用互斥锁确保并发安全
func (w *ConnIDGenerater) NextID() (uint64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.nextID()
}

// nextID 实现了基于雪花算法的 ID 生成逻辑
// 1. 使用时间戳的高位来确保 ID 的全局唯一性
// 2. 使用序列号的低位来确保同一毫秒内的唯一性
// 3. 处理时钟回拨和序列号溢出的情况
func (w *ConnIDGenerater) nextID() (uint64, error) {
	timeStamp := w.getMilliSeconds()
	if timeStamp < w.LastStamp {
		return 0, errors.New("time is moving backwards,waiting until")
	}

	if w.LastStamp == timeStamp {
		w.Sequence = (w.Sequence + 1) & maxSequence
		if w.Sequence == 0 { // 如果这里发生溢出，就等到下一个毫秒时再分配，这样就一定出现重复
			for timeStamp <= w.LastStamp {
				timeStamp = w.getMilliSeconds()
			}
		}
	} else { // 如果与上次分配的时间戳不等，则为了防止可能的时钟飘移现象，就必须重新计数
		w.Sequence = 0
	}
	w.LastStamp = timeStamp
	//减法可以压缩一下时间戳
	id := ((timeStamp - twepoch) << timeLeft) | w.Sequence
	connID := uint64(id) | (version << versionLeft)
	return connID, nil
}
