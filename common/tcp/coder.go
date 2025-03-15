package tcp

import (
	"bytes"
	"encoding/binary"
)

// 定义数据包结构体，包含长度和数据
type DataPgk struct {
	Len  uint32 // 数据包长度
	Data []byte // 数据包内容
}

// 将数据包结构体编码为字节数组
func (d *DataPgk) Marshal() []byte {
	bytesBuffer := bytes.NewBuffer([]byte{}) // 创建一个新的字节缓冲区
	binary.Write(bytesBuffer, binary.BigEndian, d.Len) // 将长度以大端序写入缓冲区
	return append(bytesBuffer.Bytes(), d.Data...) // 将数据追加到缓冲区并返回
}