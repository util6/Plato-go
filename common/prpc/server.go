package prpc

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hardcore-os/plato/common/prpc/discov/plugin"

	"github.com/bytedance/gopkg/util/logger"

	"github.com/hardcore-os/plato/common/prpc/discov"
	serverinterceptor "github.com/hardcore-os/plato/common/prpc/interceptor/server"
	"google.golang.org/grpc"
)

type RegisterFn func(*grpc.Server)

type PServer struct {
	serverOptions
	registers    []RegisterFn
	interceptors []grpc.UnaryServerInterceptor
}

type serverOptions struct {
	serviceName string
	ip          string
	port        int
	weight      int
	health      bool
	d           discov.Discovery
}

type ServerOption func(opts *serverOptions)

// WithServiceName set serviceName
func WithServiceName(serviceName string) ServerOption {
	return func(opts *serverOptions) {
		opts.serviceName = serviceName
	}
}

// WithIP set ip
func WithIP(ip string) ServerOption {
	return func(opts *serverOptions) {
		opts.ip = ip
	}
}

// WithPort set port
func WithPort(port int) ServerOption {
	return func(opts *serverOptions) {
		opts.port = port
	}
}

// WithWeight set weight
func WithWeight(weight int) ServerOption {
	return func(opts *serverOptions) {
		opts.weight = weight
	}
}

// WithHealth set health
func WithHealth(health bool) ServerOption {
	return func(opts *serverOptions) {
		opts.health = health
	}
}

func NewPServer(opts ...ServerOption) *PServer {
	opt := serverOptions{}
	for _, o := range opts {
		o(&opt)
	}

	if opt.d == nil {
		dis, err := plugin.GetDiscovInstance()
		if err != nil {
			panic(err)
		}

		opt.d = dis
	}

	return &PServer{
		opt,
		make([]RegisterFn, 0),
		make([]grpc.UnaryServerInterceptor, 0),
	}
}

// RegisterService ...
// eg :
// p.RegisterService(func(server *grpc.Server) {
//     test.RegisterGreeterServer(server, &Server{})
// })
func (p *PServer) RegisterService(register ...RegisterFn) {
	p.registers = append(p.registers, register...)
}

// RegisterUnaryServerInterceptor 注册一个一元服务拦截器
func (p *PServer) RegisterUnaryServerInterceptor(i grpc.UnaryServerInterceptor) {
	p.interceptors = append(p.interceptors, i) // 将拦截器添加到拦截器列表中
}

// Start 开启server
func (p *PServer) Start(ctx context.Context) {
	service := discov.Service{
		Name: p.serviceName, // 设置服务名称
		Endpoints: []*discov.Endpoint{
			{
				ServerName: p.serviceName, // 设置服务器名称
				IP:         p.ip,          // 设置服务器IP
				Port:       p.port,        // 设置服务器端口
				Weight:     p.weight,      // 设置服务器权重
				Enable:     true,          // 设置服务启用状态
			},
		},
	}

	// 加载中间件
	interceptors := []grpc.UnaryServerInterceptor{
		serverinterceptor.RecoveryUnaryServerInterceptor(), // 恢复中间件
		serverinterceptor.TraceUnaryServerInterceptor(),    // 跟踪中间件
		serverinterceptor.MetricUnaryServerInterceptor(p.serviceName), // 指标中间件
	}
	interceptors = append(interceptors, p.interceptors...) // 将自定义拦截器添加到中间件列表中

	s := grpc.NewServer(grpc.ChainUnaryInterceptor(interceptors...)) // 创建gRPC服务器并设置中间件链

	// 注册服务
	for _, register := range p.registers {
		register(s) // 注册每个服务
	}

	lis, err := net.Listen("tcp", fmt.Sprintf("%s:%d", p.ip, p.port)) // 监听指定IP和端口
	if err != nil {
		panic(err) // 如果监听失败则抛出异常
	}

	go func() {
		if err := s.Serve(lis); err != nil { // 启动gRPC服务器
			panic(err) // 如果启动失败则抛出异常
		}
	}()
	// 服务注册
	p.d.Register(ctx, &service) // 注册服务到发现中心

	logger.Info("start PRCP success") // 记录启动成功日志

	c := make(chan os.Signal, 1) // 创建信号通道
	signal.Notify(c, syscall.SIGHUP, syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGINT) // 监听系统信号
	for {
		sig := <-c // 等待信号
		switch sig {
		case syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGINT:
			s.Stop() // 停止gRPC服务器
			p.d.UnRegister(ctx, &service) // 取消服务注册
			time.Sleep(time.Second) // 等待1秒
			return // 退出
		case syscall.SIGHUP:
		default:
			return // 退出
		}
	}

}
