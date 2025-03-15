package tcp

import "net"

// 将指定数据包发送到网络中
func SendData(conn *net.TCPConn, data []byte) error {
	totalLen := len(data) // 获取数据包的总长度
	writeLen := 0         // 初始化已写入的长度为0
	for {
		len, err := conn.Write(data[writeLen:]) // 尝试写入数据
		if err != nil {
			return err // 如果发生错误，返回错误信息
		}
		writeLen = writeLen + len // 更新已写入的长度
		if writeLen >= totalLen {
			break // 如果已写入的长度大于或等于总长度，退出循环
		}
	}
	return nil // 返回nil表示成功发送数据
}