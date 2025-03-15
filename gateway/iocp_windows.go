//go:build windows
// Package gateway 实现了基于 Windows IOCP(I/O Completion Port) 的高性能网络 I/O 多路复用
// 本包主要实现以下功能：
// 1. 使用 IOCP 实现高效的 TCP 连接管理
// 2. 支持百万级并发连接
// 3. 实现连接数限制和优雅关闭
// 4. 采用多 worker 协程处理连接事件
package gateway

import (
	"fmt"
	"net"
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/hardcore-os/plato/common/config"
	"golang.org/x/sys/windows"
)

var (
	ep     *ePool    // IOCP 连接池，用于管理所有 TCP 连接
	tcpNum int32     // 当前服务器的 TCP 连接数量
)

// ePool 表示 IOCP 连接池，用于管理和调度所有 TCP 连接
type ePool struct {
	eChan  chan *NIOConnection // 新连接通道
	tables sync.Map        // 连接 ID 到连接对象的映射表
	eSize  int            // IOCP 实例数量
	done   chan struct{}  // 关闭信号通道

	ln *net.TCPListener                    // TCP 监听器
	f  func(c *NIOConnection, ep *epoller)    // 连接处理回调函数
}

// initEpoll 初始化 IOCP 连接池
// ln: TCP 监听器
// f: 连接处理回调函数
func initEpoll(ln *net.TCPListener, f func(c *NIOConnection, ep *epoller)) {
	ep = newEPool(ln, f)
	ep.createAcceptProcess() // 创建 Accept 处理协程
	ep.startEPool()         // 启动 IOCP 处理协程池
}

// newEPool 创建新的 IOCP 连接池
// ln: TCP 监听器
// cb: 连接处理回调函数
func newEPool(ln *net.TCPListener, cb func(c *NIOConnection, ep *epoller)) *ePool {
	return &ePool{
		eChan:  make(chan *NIOConnection, config.GetGatewayEpollerChanNum()), // 新连接通道
		done:   make(chan struct{}),                                      // 关闭信号通道
		eSize:  config.GetGatewayEpollerNum(),                           // IOCP 实例数量
		tables: sync.Map{},                                              // 连接映射表
		ln:     ln,                                                      // TCP 监听器
		f:      cb,                                                      // 连接处理回调
	}
}

// createAcceptProcess 创建 Accept 处理协程
// 根据 CPU 核心数创建对应数量的协程处理新连接
func (e *ePool) createAcceptProcess() {
	for i := 0; i < runtime.NumCPU(); i++ {
		go func() {
			for {
				conn, e := e.ln.AcceptTCP() // 接受新的 TCP 连接
				// 连接数限制检查
				if !checkTcp() {
					_ = conn.Close()
					continue
				}
				setTcpConifg(conn) // 设置 TCP 连接参数
				if e != nil {
					// 处理临时错误
					if ne, ok := e.(net.Error); ok && ne.Temporary() {
						fmt.Errorf("accept temp err: %v", ne)
						continue
					}
					fmt.Errorf("accept err: %v", e)
				}
				c := NewConnection(conn) // 创建新的连接对象
				ep.addTask(c)          // 添加到任务队列
			}
		}()
	}
}

// startEPool 启动 IOCP 处理协程池
// 创建多个 IOCP 实例，每个实例由一个协程处理
func (e *ePool) startEPool() {
	for i := 0; i < e.eSize; i++ {
		go e.startEProc()
	}
}

// startEProc 启动单个 IOCP 处理协程
// 包含两个主要功能：
// 1. 处理新连接的加入
// 2. 处理已有连接的 I/O 事件
func (e *ePool) startEProc() {
	ep, err := newEpoller()
	if err != nil {
		panic(err)
	}

	// 处理新连接
	go func() {
		for {
			select {
			case <-e.done:
				return
			case conn := <-e.eChan:
				addTcpNum()
				fmt.Printf("tcpNum:%d\n", tcpNum)
				if err := ep.add(conn); err != nil {
					fmt.Printf("failed to add NIOConnection %v\n", err)
					conn.Close()
					continue
				}
				fmt.Printf("EpollerPool new NIOConnection[%v] tcpSize:%d\n", conn.RemoteAddr(), tcpNum)
			}
		}
	}()

	// 处理 I/O 事件
	for {
		select {
		case <-e.done:
			return
		default:
			// 使用 200ms 超时避免忙轮询
			NIOConnections, err := ep.wait(200)
			if err != nil {
				fmt.Printf("failed to wait %v\n", err)
				continue
			}
			for _, conn := range NIOConnections {
				if conn == nil {
					break
				}
				e.f(conn, ep) // 调用回调处理连接
			}
		}
	}
}

// addTask 添加新连接到任务队列
func (e *ePool) addTask(c *NIOConnection) {
	e.eChan <- c
}

// epoller 表示一个 IOCP 实例
type epoller struct {
	iocp          windows.Handle // IOCP 句柄
	fdToConnTable sync.Map       // 文件描述符到连接对象的映射表
}

// newEpoller 创建新的 IOCP 实例
func newEpoller() (*epoller, error) {
	iocp, err := windows.CreateIoCompletionPort(windows.InvalidHandle, 0, 0, 0)
	if err != nil {
		return nil, fmt.Errorf("CreateIoCompletionPort failed: %v", err)
	}
	return &epoller{
		iocp: iocp,
	}, nil
}

// add 将连接关联到 IOCP 实例
func (e *epoller) add(conn *NIOConnection) error {
	fd := windows.Handle(conn.fd)
	_, err := windows.CreateIoCompletionPort(fd, e.iocp, 0, 0)
	if err != nil {
		return fmt.Errorf("CreateIoCompletionPort failed: %v", err)
	}
	e.fdToConnTable.Store(conn.fd, conn)
	ep.tables.Store(conn.id, conn)
	conn.BindEpoller(e)
	return nil
}

// remove 从 IOCP 实例中移除连接
func (e *epoller) remove(c *NIOConnection) error {
	subTcpNum()
	e.fdToConnTable.Delete(c.fd)
	ep.tables.Delete(c.id)
	return nil
}

// wait 等待 IOCP 完成事件
// msec: 超时时间(毫秒)
// 返回发生事件的连接列表
func (e *epoller) wait(msec int) ([]*NIOConnection, error) {
	var n uint32
	var key uintptr
	var overlapped *windows.Overlapped

	err := windows.GetQueuedCompletionStatus(e.iocp, &n, &key, &overlapped, uint32(msec))
	if err != nil {
		if err == windows.WAIT_TIMEOUT {
			return nil, nil
		}
		return nil, err
	}

	if conn, ok := e.fdToConnTable.Load(int(key)); ok {
		return []*NIOConnection{conn.(*NIOConnection)}, nil
	}
	return nil, nil
}

// addTcpNum 增加 TCP 连接计数
func addTcpNum() {
	atomic.AddInt32(&tcpNum, 1)
}

// getTcpNum 获取当前 TCP 连接数
func getTcpNum() int32 {
	return atomic.LoadInt32(&tcpNum)
}

// subTcpNum 减少 TCP 连接计数
func subTcpNum() {
	atomic.AddInt32(&tcpNum, -1)
}

// checkTcp 检查是否超过最大连接数限制
func checkTcp() bool {
	num := getTcpNum()
	maxTcpNum := config.GetGatewayMaxTcpNum()
	return num <= maxTcpNum
}

// setTcpConifg 设置 TCP 连接参数
// 启用 TCP keepalive
func setTcpConifg(c *net.TCPConn) {
	_ = c.SetKeepAlive(true)
}