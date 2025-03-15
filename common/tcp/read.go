package tcp

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net"
	"time"
)

// 从网络连接中读取数据
func ReadData(conn *net.TCPConn) ([]byte, error) {
	var dataLen uint32 // 定义数据包长度变量
	dataLenBuf := make([]byte, 4) // 创建一个长度为4的字节数组用于存储数据包长度
	if err := readFixedData(conn, dataLenBuf); err != nil { // 读取数据包长度
		return nil, err // 如果发生错误，返回错误信息
	}
	buffer := bytes.NewBuffer(dataLenBuf) // 创建一个新的字节缓冲区
	if err := binary.Read(buffer, binary.BigEndian, &dataLen); err != nil { // 从缓冲区读取数据包长度
		return nil, fmt.Errorf("read headlen error:%s", err.Error()) // 如果发生错误，返回错误信息
	}
	if dataLen <= 0 {
		return nil, fmt.Errorf("wrong headlen :%d", dataLen) // 如果数据包长度小于等于0，返回错误信息
	}
	dataBuf := make([]byte, dataLen) // 创建一个长度为dataLen的字节数组用于存储数据包内容
	if err := readFixedData(conn, dataBuf); err != nil { // 读取数据包内容
		return nil, fmt.Errorf("read headlen error:%s", err.Error()) // 如果发生错误，返回错误信息
	}
	return dataBuf, nil // 返回读取到的数据包内容
}

// 读取固定buf长度的数据
func readFixedData(conn *net.TCPConn, buf []byte) error {
	_ = (*conn).SetReadDeadline(time.Now().Add(time.Duration(120) * time.Second)) // 设置读取超时时间为120秒
	var pos int = 0 // 初始化当前位置为0
	var totalSize int = len(buf) // 获取buf的总长度
	for {
		c, err := (*conn).Read(buf[pos:]) // 从连接中读取数据
		if err != nil {
			return err // 如果发生错误，返回错误信息
		}
		pos = pos + c // 更新当前位置
		if pos == totalSize {
			break // 如果当前位置等于buf的总长度，退出循环
		}
	}
	return nil // 返回nil表示成功读取数据
}