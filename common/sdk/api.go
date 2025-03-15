package sdk

import (
	"net"
	"sync"
	"time"

	"github.com/goccy/go-json"
	"github.com/hardcore-os/plato/common/idl/message"
	"github.com/hardcore-os/plato/common/tcp"
	"google.golang.org/protobuf/proto"
)

// 定义消息类型常量
const (
	MsgTypeText      = "text"      // 文本消息类型
	MsgTypeAck       = "ack"       // 确认消息类型
	MsgTypeReConn    = "reConn"    // 重连消息类型
	MsgTypeHeartbeat = "heartbeat" // 心跳消息类型
	MsgLogin         = "loginMsg"  // 登录消息类型
)

// Chat 结构体定义聊天客户端
type Chat struct {
	Nick             string                     // 用户昵称
	UserID           string                     // 用户ID
	SessionID        string                     // 会话ID
	conn             *connect                   // 连接对象
	closeChan        chan struct{}                // 关闭通道
	MsgClientIDTable map[string]uint64            // 消息客户端ID表
	sync.RWMutex                             // 读写锁
}

// Message 结构体定义消息格式
type Message struct {
	Type       string // 消息类型
	Name       string // 发送者名称
	FormUserID string // 发送者用户ID
	ToUserID   string // 接收者用户ID
	Content    string // 消息内容
	Session    string // 会话ID
}

// NewChat 创建并初始化Chat实例
func NewChat(ip net.IP, port int, nick, userID, sessionID string) *Chat {
	chat := &Chat{
		Nick:             nick,             // 设置用户昵称
		UserID:           userID,           // 设置用户ID
		SessionID:        sessionID,        // 设置会话ID
		conn:             newConnet(ip, port), // 创建连接对象
		closeChan:        make(chan struct{}), // 创建关闭通道
		MsgClientIDTable: make(map[string]uint64), // 初始化消息客户端ID表
	}
	go chat.loop() // 启动循环处理
	chat.login() // 登录
	go chat.heartbeat() // 启动心跳
	return chat
}

// Send 发送消息
func (chat *Chat) Send(msg *Message) {
	data, _ := json.Marshal(msg) // 将消息结构体序列化为JSON
	upMsg := &message.UPMsg{
		Head: &message.UPMsgHead{
			ClientID:  chat.getClientID(chat.SessionID), // 设置客户端ID
			ConnID:    chat.conn.connID, // 设置连接ID
			SessionId: chat.SessionID, // 设置会话ID
		},
		UPMsgBody: data, // 设置消息体
	}
	palyload, _ := proto.Marshal(upMsg) // 将上行消息结构体序列化为protobuf
	chat.conn.send(message.CmdType_UP, palyload) // 发送消息
}

// GetCurClientID 获取当前客户端ID
func (chat *Chat) GetCurClientID() uint64 {
	if id, ok := chat.MsgClientIDTable[chat.SessionID]; ok { // 检查会话ID是否存在
		return id // 返回客户端ID
	}
	return 0 // 返回0表示未找到
}

// Close 关闭聊天客户端
func (chat *Chat) Close() {
	chat.conn.close() // 关闭连接
	close(chat.closeChan) // 关闭通道
	close(chat.conn.recvChan) // 关闭接收通道
	close(chat.conn.sendChan) // 关闭发送通道
}

// ReConn 重新连接服务器
func (chat *Chat) ReConn() {
	chat.Lock() // 加锁
	defer chat.Unlock() // 解锁
	chat.MsgClientIDTable = make(map[string]uint64) // 重置消息客户端ID表
	chat.conn.reConn() // 重新连接
	chat.reConn() // 重新连接
}

// Recv 接收消息