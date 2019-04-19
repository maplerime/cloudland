// Code generated by protoc-gen-go. DO NOT EDIT.
// source: dbs.proto

package dbs

import (
	context "context"
	fmt "fmt"
	proto "github.com/golang/protobuf/proto"
	grpc "google.golang.org/grpc"
	codes "google.golang.org/grpc/codes"
	status "google.golang.org/grpc/status"
	math "math"
)

// Reference imports to suppress errors if they are not otherwise used.
var _ = proto.Marshal
var _ = fmt.Errorf
var _ = math.Inf

// This is a compile-time assertion to ensure that this generated file
// is compatible with the proto package it is being compiled against.
// A compilation error at this line likely means your copy of the
// proto package needs to be updated.
const _ = proto.ProtoPackageIsVersion3 // please upgrade the proto package

type StatsRequest struct {
	XXX_NoUnkeyedLiteral struct{} `json:"-"`
	XXX_unrecognized     []byte   `json:"-"`
	XXX_sizecache        int32    `json:"-"`
}

func (m *StatsRequest) Reset()         { *m = StatsRequest{} }
func (m *StatsRequest) String() string { return proto.CompactTextString(m) }
func (*StatsRequest) ProtoMessage()    {}
func (*StatsRequest) Descriptor() ([]byte, []int) {
	return fileDescriptor_e975d2518bdd2efa, []int{0}
}

func (m *StatsRequest) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_StatsRequest.Unmarshal(m, b)
}
func (m *StatsRequest) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_StatsRequest.Marshal(b, m, deterministic)
}
func (m *StatsRequest) XXX_Merge(src proto.Message) {
	xxx_messageInfo_StatsRequest.Merge(m, src)
}
func (m *StatsRequest) XXX_Size() int {
	return xxx_messageInfo_StatsRequest.Size(m)
}
func (m *StatsRequest) XXX_DiscardUnknown() {
	xxx_messageInfo_StatsRequest.DiscardUnknown(m)
}

var xxx_messageInfo_StatsRequest proto.InternalMessageInfo

type Table struct {
	Rows                 int64    `protobuf:"varint,2,opt,name=rows,proto3" json:"rows,omitempty"`
	Deleted              int64    `protobuf:"varint,3,opt,name=deleted,proto3" json:"deleted,omitempty"`
	Error                string   `protobuf:"bytes,4,opt,name=error,proto3" json:"error,omitempty"`
	XXX_NoUnkeyedLiteral struct{} `json:"-"`
	XXX_unrecognized     []byte   `json:"-"`
	XXX_sizecache        int32    `json:"-"`
}

func (m *Table) Reset()         { *m = Table{} }
func (m *Table) String() string { return proto.CompactTextString(m) }
func (*Table) ProtoMessage()    {}
func (*Table) Descriptor() ([]byte, []int) {
	return fileDescriptor_e975d2518bdd2efa, []int{1}
}

func (m *Table) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_Table.Unmarshal(m, b)
}
func (m *Table) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_Table.Marshal(b, m, deterministic)
}
func (m *Table) XXX_Merge(src proto.Message) {
	xxx_messageInfo_Table.Merge(m, src)
}
func (m *Table) XXX_Size() int {
	return xxx_messageInfo_Table.Size(m)
}
func (m *Table) XXX_DiscardUnknown() {
	xxx_messageInfo_Table.DiscardUnknown(m)
}

var xxx_messageInfo_Table proto.InternalMessageInfo

func (m *Table) GetRows() int64 {
	if m != nil {
		return m.Rows
	}
	return 0
}

func (m *Table) GetDeleted() int64 {
	if m != nil {
		return m.Deleted
	}
	return 0
}

func (m *Table) GetError() string {
	if m != nil {
		return m.Error
	}
	return ""
}

type Stats struct {
	MaxOpenConnections   int64    `protobuf:"varint,1,opt,name=max_open_connections,json=maxOpenConnections,proto3" json:"max_open_connections,omitempty"`
	OpenConnections      int64    `protobuf:"varint,2,opt,name=open_connections,json=openConnections,proto3" json:"open_connections,omitempty"`
	InUse                int64    `protobuf:"varint,3,opt,name=in_use,json=inUse,proto3" json:"in_use,omitempty"`
	Idle                 int64    `protobuf:"varint,4,opt,name=idle,proto3" json:"idle,omitempty"`
	WaitCount            int64    `protobuf:"varint,5,opt,name=wait_count,json=waitCount,proto3" json:"wait_count,omitempty"`
	WaitDuration         int64    `protobuf:"varint,6,opt,name=wait_duration,json=waitDuration,proto3" json:"wait_duration,omitempty"`
	MaxIdleClosed        int64    `protobuf:"varint,7,opt,name=max_idle_closed,json=maxIdleClosed,proto3" json:"max_idle_closed,omitempty"`
	MaxLifetimeClosed    int64    `protobuf:"varint,8,opt,name=max_lifetime_closed,json=maxLifetimeClosed,proto3" json:"max_lifetime_closed,omitempty"`
	XXX_NoUnkeyedLiteral struct{} `json:"-"`
	XXX_unrecognized     []byte   `json:"-"`
	XXX_sizecache        int32    `json:"-"`
}

func (m *Stats) Reset()         { *m = Stats{} }
func (m *Stats) String() string { return proto.CompactTextString(m) }
func (*Stats) ProtoMessage()    {}
func (*Stats) Descriptor() ([]byte, []int) {
	return fileDescriptor_e975d2518bdd2efa, []int{2}
}

func (m *Stats) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_Stats.Unmarshal(m, b)
}
func (m *Stats) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_Stats.Marshal(b, m, deterministic)
}
func (m *Stats) XXX_Merge(src proto.Message) {
	xxx_messageInfo_Stats.Merge(m, src)
}
func (m *Stats) XXX_Size() int {
	return xxx_messageInfo_Stats.Size(m)
}
func (m *Stats) XXX_DiscardUnknown() {
	xxx_messageInfo_Stats.DiscardUnknown(m)
}

var xxx_messageInfo_Stats proto.InternalMessageInfo

func (m *Stats) GetMaxOpenConnections() int64 {
	if m != nil {
		return m.MaxOpenConnections
	}
	return 0
}

func (m *Stats) GetOpenConnections() int64 {
	if m != nil {
		return m.OpenConnections
	}
	return 0
}

func (m *Stats) GetInUse() int64 {
	if m != nil {
		return m.InUse
	}
	return 0
}

func (m *Stats) GetIdle() int64 {
	if m != nil {
		return m.Idle
	}
	return 0
}

func (m *Stats) GetWaitCount() int64 {
	if m != nil {
		return m.WaitCount
	}
	return 0
}

func (m *Stats) GetWaitDuration() int64 {
	if m != nil {
		return m.WaitDuration
	}
	return 0
}

func (m *Stats) GetMaxIdleClosed() int64 {
	if m != nil {
		return m.MaxIdleClosed
	}
	return 0
}

func (m *Stats) GetMaxLifetimeClosed() int64 {
	if m != nil {
		return m.MaxLifetimeClosed
	}
	return 0
}

type StatsReply struct {
	Stats                *Stats   `protobuf:"bytes,1,opt,name=stats,proto3" json:"stats,omitempty"`
	XXX_NoUnkeyedLiteral struct{} `json:"-"`
	XXX_unrecognized     []byte   `json:"-"`
	XXX_sizecache        int32    `json:"-"`
}

func (m *StatsReply) Reset()         { *m = StatsReply{} }
func (m *StatsReply) String() string { return proto.CompactTextString(m) }
func (*StatsReply) ProtoMessage()    {}
func (*StatsReply) Descriptor() ([]byte, []int) {
	return fileDescriptor_e975d2518bdd2efa, []int{3}
}

func (m *StatsReply) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_StatsReply.Unmarshal(m, b)
}
func (m *StatsReply) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_StatsReply.Marshal(b, m, deterministic)
}
func (m *StatsReply) XXX_Merge(src proto.Message) {
	xxx_messageInfo_StatsReply.Merge(m, src)
}
func (m *StatsReply) XXX_Size() int {
	return xxx_messageInfo_StatsReply.Size(m)
}
func (m *StatsReply) XXX_DiscardUnknown() {
	xxx_messageInfo_StatsReply.DiscardUnknown(m)
}

var xxx_messageInfo_StatsReply proto.InternalMessageInfo

func (m *StatsReply) GetStats() *Stats {
	if m != nil {
		return m.Stats
	}
	return nil
}

type TablesRequest struct {
	XXX_NoUnkeyedLiteral struct{} `json:"-"`
	XXX_unrecognized     []byte   `json:"-"`
	XXX_sizecache        int32    `json:"-"`
}

func (m *TablesRequest) Reset()         { *m = TablesRequest{} }
func (m *TablesRequest) String() string { return proto.CompactTextString(m) }
func (*TablesRequest) ProtoMessage()    {}
func (*TablesRequest) Descriptor() ([]byte, []int) {
	return fileDescriptor_e975d2518bdd2efa, []int{4}
}

func (m *TablesRequest) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_TablesRequest.Unmarshal(m, b)
}
func (m *TablesRequest) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_TablesRequest.Marshal(b, m, deterministic)
}
func (m *TablesRequest) XXX_Merge(src proto.Message) {
	xxx_messageInfo_TablesRequest.Merge(m, src)
}
func (m *TablesRequest) XXX_Size() int {
	return xxx_messageInfo_TablesRequest.Size(m)
}
func (m *TablesRequest) XXX_DiscardUnknown() {
	xxx_messageInfo_TablesRequest.DiscardUnknown(m)
}

var xxx_messageInfo_TablesRequest proto.InternalMessageInfo

type TablesReply struct {
	Tables               map[string]*Table `protobuf:"bytes,1,rep,name=tables,proto3" json:"tables,omitempty" protobuf_key:"bytes,1,opt,name=key,proto3" protobuf_val:"bytes,2,opt,name=value,proto3"`
	XXX_NoUnkeyedLiteral struct{}          `json:"-"`
	XXX_unrecognized     []byte            `json:"-"`
	XXX_sizecache        int32             `json:"-"`
}

func (m *TablesReply) Reset()         { *m = TablesReply{} }
func (m *TablesReply) String() string { return proto.CompactTextString(m) }
func (*TablesReply) ProtoMessage()    {}
func (*TablesReply) Descriptor() ([]byte, []int) {
	return fileDescriptor_e975d2518bdd2efa, []int{5}
}

func (m *TablesReply) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_TablesReply.Unmarshal(m, b)
}
func (m *TablesReply) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_TablesReply.Marshal(b, m, deterministic)
}
func (m *TablesReply) XXX_Merge(src proto.Message) {
	xxx_messageInfo_TablesReply.Merge(m, src)
}
func (m *TablesReply) XXX_Size() int {
	return xxx_messageInfo_TablesReply.Size(m)
}
func (m *TablesReply) XXX_DiscardUnknown() {
	xxx_messageInfo_TablesReply.DiscardUnknown(m)
}

var xxx_messageInfo_TablesReply proto.InternalMessageInfo

func (m *TablesReply) GetTables() map[string]*Table {
	if m != nil {
		return m.Tables
	}
	return nil
}

type ExecRequest struct {
	Sql                  string   `protobuf:"bytes,1,opt,name=sql,proto3" json:"sql,omitempty"`
	XXX_NoUnkeyedLiteral struct{} `json:"-"`
	XXX_unrecognized     []byte   `json:"-"`
	XXX_sizecache        int32    `json:"-"`
}

func (m *ExecRequest) Reset()         { *m = ExecRequest{} }
func (m *ExecRequest) String() string { return proto.CompactTextString(m) }
func (*ExecRequest) ProtoMessage()    {}
func (*ExecRequest) Descriptor() ([]byte, []int) {
	return fileDescriptor_e975d2518bdd2efa, []int{6}
}

func (m *ExecRequest) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_ExecRequest.Unmarshal(m, b)
}
func (m *ExecRequest) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_ExecRequest.Marshal(b, m, deterministic)
}
func (m *ExecRequest) XXX_Merge(src proto.Message) {
	xxx_messageInfo_ExecRequest.Merge(m, src)
}
func (m *ExecRequest) XXX_Size() int {
	return xxx_messageInfo_ExecRequest.Size(m)
}
func (m *ExecRequest) XXX_DiscardUnknown() {
	xxx_messageInfo_ExecRequest.DiscardUnknown(m)
}

var xxx_messageInfo_ExecRequest proto.InternalMessageInfo

func (m *ExecRequest) GetSql() string {
	if m != nil {
		return m.Sql
	}
	return ""
}

type ExecReply struct {
	Affected             int64    `protobuf:"varint,1,opt,name=affected,proto3" json:"affected,omitempty"`
	XXX_NoUnkeyedLiteral struct{} `json:"-"`
	XXX_unrecognized     []byte   `json:"-"`
	XXX_sizecache        int32    `json:"-"`
}

func (m *ExecReply) Reset()         { *m = ExecReply{} }
func (m *ExecReply) String() string { return proto.CompactTextString(m) }
func (*ExecReply) ProtoMessage()    {}
func (*ExecReply) Descriptor() ([]byte, []int) {
	return fileDescriptor_e975d2518bdd2efa, []int{7}
}

func (m *ExecReply) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_ExecReply.Unmarshal(m, b)
}
func (m *ExecReply) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_ExecReply.Marshal(b, m, deterministic)
}
func (m *ExecReply) XXX_Merge(src proto.Message) {
	xxx_messageInfo_ExecReply.Merge(m, src)
}
func (m *ExecReply) XXX_Size() int {
	return xxx_messageInfo_ExecReply.Size(m)
}
func (m *ExecReply) XXX_DiscardUnknown() {
	xxx_messageInfo_ExecReply.DiscardUnknown(m)
}

var xxx_messageInfo_ExecReply proto.InternalMessageInfo

func (m *ExecReply) GetAffected() int64 {
	if m != nil {
		return m.Affected
	}
	return 0
}

type QueryRequest struct {
	Sql                  string   `protobuf:"bytes,1,opt,name=sql,proto3" json:"sql,omitempty"`
	XXX_NoUnkeyedLiteral struct{} `json:"-"`
	XXX_unrecognized     []byte   `json:"-"`
	XXX_sizecache        int32    `json:"-"`
}

func (m *QueryRequest) Reset()         { *m = QueryRequest{} }
func (m *QueryRequest) String() string { return proto.CompactTextString(m) }
func (*QueryRequest) ProtoMessage()    {}
func (*QueryRequest) Descriptor() ([]byte, []int) {
	return fileDescriptor_e975d2518bdd2efa, []int{8}
}

func (m *QueryRequest) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_QueryRequest.Unmarshal(m, b)
}
func (m *QueryRequest) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_QueryRequest.Marshal(b, m, deterministic)
}
func (m *QueryRequest) XXX_Merge(src proto.Message) {
	xxx_messageInfo_QueryRequest.Merge(m, src)
}
func (m *QueryRequest) XXX_Size() int {
	return xxx_messageInfo_QueryRequest.Size(m)
}
func (m *QueryRequest) XXX_DiscardUnknown() {
	xxx_messageInfo_QueryRequest.DiscardUnknown(m)
}

var xxx_messageInfo_QueryRequest proto.InternalMessageInfo

func (m *QueryRequest) GetSql() string {
	if m != nil {
		return m.Sql
	}
	return ""
}

type Result struct {
	Values               []string `protobuf:"bytes,1,rep,name=values,proto3" json:"values,omitempty"`
	XXX_NoUnkeyedLiteral struct{} `json:"-"`
	XXX_unrecognized     []byte   `json:"-"`
	XXX_sizecache        int32    `json:"-"`
}

func (m *Result) Reset()         { *m = Result{} }
func (m *Result) String() string { return proto.CompactTextString(m) }
func (*Result) ProtoMessage()    {}
func (*Result) Descriptor() ([]byte, []int) {
	return fileDescriptor_e975d2518bdd2efa, []int{9}
}

func (m *Result) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_Result.Unmarshal(m, b)
}
func (m *Result) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_Result.Marshal(b, m, deterministic)
}
func (m *Result) XXX_Merge(src proto.Message) {
	xxx_messageInfo_Result.Merge(m, src)
}
func (m *Result) XXX_Size() int {
	return xxx_messageInfo_Result.Size(m)
}
func (m *Result) XXX_DiscardUnknown() {
	xxx_messageInfo_Result.DiscardUnknown(m)
}

var xxx_messageInfo_Result proto.InternalMessageInfo

func (m *Result) GetValues() []string {
	if m != nil {
		return m.Values
	}
	return nil
}

type QueryReply struct {
	Result               []*Result `protobuf:"bytes,1,rep,name=result,proto3" json:"result,omitempty"`
	XXX_NoUnkeyedLiteral struct{}  `json:"-"`
	XXX_unrecognized     []byte    `json:"-"`
	XXX_sizecache        int32     `json:"-"`
}

func (m *QueryReply) Reset()         { *m = QueryReply{} }
func (m *QueryReply) String() string { return proto.CompactTextString(m) }
func (*QueryReply) ProtoMessage()    {}
func (*QueryReply) Descriptor() ([]byte, []int) {
	return fileDescriptor_e975d2518bdd2efa, []int{10}
}

func (m *QueryReply) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_QueryReply.Unmarshal(m, b)
}
func (m *QueryReply) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_QueryReply.Marshal(b, m, deterministic)
}
func (m *QueryReply) XXX_Merge(src proto.Message) {
	xxx_messageInfo_QueryReply.Merge(m, src)
}
func (m *QueryReply) XXX_Size() int {
	return xxx_messageInfo_QueryReply.Size(m)
}
func (m *QueryReply) XXX_DiscardUnknown() {
	xxx_messageInfo_QueryReply.DiscardUnknown(m)
}

var xxx_messageInfo_QueryReply proto.InternalMessageInfo

func (m *QueryReply) GetResult() []*Result {
	if m != nil {
		return m.Result
	}
	return nil
}

func init() {
	proto.RegisterType((*StatsRequest)(nil), "com.gabecloud.sca.dbs.StatsRequest")
	proto.RegisterType((*Table)(nil), "com.gabecloud.sca.dbs.Table")
	proto.RegisterType((*Stats)(nil), "com.gabecloud.sca.dbs.Stats")
	proto.RegisterType((*StatsReply)(nil), "com.gabecloud.sca.dbs.StatsReply")
	proto.RegisterType((*TablesRequest)(nil), "com.gabecloud.sca.dbs.TablesRequest")
	proto.RegisterType((*TablesReply)(nil), "com.gabecloud.sca.dbs.TablesReply")
	proto.RegisterMapType((map[string]*Table)(nil), "com.gabecloud.sca.dbs.TablesReply.TablesEntry")
	proto.RegisterType((*ExecRequest)(nil), "com.gabecloud.sca.dbs.ExecRequest")
	proto.RegisterType((*ExecReply)(nil), "com.gabecloud.sca.dbs.ExecReply")
	proto.RegisterType((*QueryRequest)(nil), "com.gabecloud.sca.dbs.QueryRequest")
	proto.RegisterType((*Result)(nil), "com.gabecloud.sca.dbs.Result")
	proto.RegisterType((*QueryReply)(nil), "com.gabecloud.sca.dbs.QueryReply")
}

func init() { proto.RegisterFile("dbs.proto", fileDescriptor_e975d2518bdd2efa) }

var fileDescriptor_e975d2518bdd2efa = []byte{
	// 577 bytes of a gzipped FileDescriptorProto
	0x1f, 0x8b, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00, 0x02, 0xff, 0x84, 0x54, 0xd1, 0x6e, 0xd3, 0x30,
	0x14, 0x5d, 0x9a, 0x25, 0x5b, 0x6e, 0x37, 0x36, 0xcc, 0x86, 0xa2, 0x8a, 0x89, 0xe0, 0x21, 0x18,
	0x2f, 0x11, 0x2a, 0x42, 0x42, 0x3c, 0xc1, 0xba, 0x21, 0x21, 0x10, 0x68, 0x61, 0x08, 0x89, 0x97,
	0xca, 0x49, 0x3c, 0x14, 0xe1, 0xc4, 0x5d, 0xec, 0xb0, 0xf6, 0x9b, 0x90, 0xf8, 0x1b, 0xfe, 0x07,
	0xf9, 0x3a, 0xed, 0x2a, 0x20, 0xdd, 0x9b, 0xef, 0xb9, 0xc7, 0xa7, 0xe7, 0xde, 0xe3, 0x06, 0x82,
	0x3c, 0x55, 0xf1, 0xa4, 0x96, 0x5a, 0x92, 0xfd, 0x4c, 0x96, 0xf1, 0x37, 0x96, 0xf2, 0x4c, 0xc8,
	0x26, 0x8f, 0x55, 0xc6, 0xe2, 0x3c, 0x55, 0xf4, 0x16, 0x6c, 0x7d, 0xd2, 0x4c, 0xab, 0x84, 0x5f,
	0x36, 0x5c, 0x69, 0xfa, 0x0e, 0xbc, 0x73, 0x96, 0x0a, 0x4e, 0x08, 0xac, 0xd7, 0xf2, 0x4a, 0x85,
	0xbd, 0xc8, 0x39, 0x72, 0x13, 0x3c, 0x93, 0x10, 0x36, 0x72, 0x2e, 0xb8, 0xe6, 0x79, 0xe8, 0x22,
	0x3c, 0x2f, 0xc9, 0x1e, 0x78, 0xbc, 0xae, 0x65, 0x1d, 0xae, 0x47, 0xce, 0x51, 0x90, 0xd8, 0x82,
	0xfe, 0xec, 0x81, 0x87, 0xea, 0xe4, 0x29, 0xec, 0x95, 0x6c, 0x3a, 0x96, 0x13, 0x5e, 0x8d, 0x33,
	0x59, 0x55, 0x3c, 0xd3, 0x85, 0xac, 0x54, 0xe8, 0xa0, 0x0c, 0x29, 0xd9, 0xf4, 0xe3, 0x84, 0x57,
	0xa3, 0xeb, 0x0e, 0x79, 0x02, 0xbb, 0xff, 0xb0, 0xad, 0x97, 0x1d, 0xf9, 0x17, 0x75, 0x1f, 0xfc,
	0xa2, 0x1a, 0x37, 0x8a, 0xb7, 0xae, 0xbc, 0xa2, 0xfa, 0xac, 0x70, 0x82, 0x22, 0x17, 0x1c, 0x2d,
	0xb9, 0x09, 0x9e, 0xc9, 0x01, 0xc0, 0x15, 0x2b, 0xf4, 0x38, 0x93, 0x4d, 0xa5, 0x43, 0x0f, 0x3b,
	0x81, 0x41, 0x46, 0x06, 0x20, 0x87, 0xb0, 0x8d, 0xed, 0xbc, 0xa9, 0x99, 0xd1, 0x0e, 0x7d, 0x64,
	0x6c, 0x19, 0xf0, 0xa4, 0xc5, 0xc8, 0x23, 0xd8, 0x31, 0xb3, 0x18, 0xbd, 0x71, 0x26, 0xa4, 0xe2,
	0x79, 0xb8, 0x81, 0xb4, 0xed, 0x92, 0x4d, 0xdf, 0xe6, 0x82, 0x8f, 0x10, 0x24, 0x31, 0xdc, 0x31,
	0x3c, 0x51, 0x5c, 0x70, 0x5d, 0x94, 0x0b, 0xee, 0x26, 0x72, 0x6f, 0x97, 0x6c, 0xfa, 0xbe, 0xed,
	0x58, 0x3e, 0x7d, 0x05, 0xd0, 0x46, 0x31, 0x11, 0x33, 0x32, 0x04, 0x4f, 0x99, 0x0a, 0x57, 0xd4,
	0x1f, 0xde, 0x8b, 0xff, 0x9b, 0x5f, 0x6c, 0x6f, 0x58, 0x2a, 0xdd, 0x81, 0x6d, 0x0c, 0x6f, 0x91,
	0xe6, 0x2f, 0x07, 0xfa, 0x73, 0xc4, 0x88, 0xbe, 0x01, 0x5f, 0x63, 0x19, 0x3a, 0x91, 0x7b, 0xd4,
	0x1f, 0xc6, 0x1d, 0xaa, 0x4b, 0x77, 0xda, 0xf3, 0x69, 0xa5, 0xeb, 0x59, 0xd2, 0xde, 0x1e, 0x7c,
	0x99, 0xcb, 0x22, 0x4c, 0x76, 0xc1, 0xfd, 0xce, 0x67, 0xe8, 0x34, 0x48, 0xcc, 0xd1, 0xb8, 0xff,
	0xc1, 0x44, 0xc3, 0x31, 0xb2, 0x6e, 0xf7, 0x28, 0x92, 0x58, 0xea, 0xcb, 0xde, 0x0b, 0x87, 0xde,
	0x87, 0xfe, 0xe9, 0x94, 0x67, 0xad, 0x7f, 0x23, 0xac, 0x2e, 0xc5, 0x5c, 0x58, 0x5d, 0x0a, 0xfa,
	0x18, 0x02, 0x4b, 0x30, 0xe3, 0x0c, 0x60, 0x93, 0x5d, 0x5c, 0xf0, 0xcc, 0x3c, 0x48, 0xfb, 0x92,
	0x16, 0x35, 0x8d, 0x60, 0xeb, 0xac, 0xe1, 0xf5, 0xac, 0x5b, 0x2a, 0x02, 0x3f, 0xe1, 0xaa, 0x11,
	0x9a, 0xdc, 0x05, 0x1f, 0x2d, 0xd8, 0xb5, 0x04, 0x49, 0x5b, 0xd1, 0x11, 0x40, 0xab, 0x61, 0x7e,
	0xed, 0x39, 0xf8, 0x35, 0xf2, 0xdb, 0xe5, 0x1d, 0x74, 0x0c, 0x65, 0x45, 0x93, 0x96, 0x3c, 0xfc,
	0xdd, 0x83, 0x8d, 0x93, 0xe3, 0xd7, 0x79, 0x59, 0x54, 0xe4, 0x6c, 0xfe, 0x7f, 0x38, 0x5c, 0x19,
	0xa7, 0xb5, 0x3c, 0x78, 0xb0, 0x9a, 0x34, 0x11, 0x33, 0xba, 0x46, 0xce, 0xc1, 0xb7, 0x51, 0x90,
	0x87, 0x37, 0x84, 0x69, 0x45, 0xe9, 0xcd, 0x91, 0xd3, 0x35, 0xf2, 0x01, 0xd6, 0xcd, 0x9a, 0x49,
	0x17, 0x7b, 0x29, 0xa4, 0x41, 0xb4, 0x92, 0x63, 0xf5, 0xce, 0xc0, 0xc3, 0x4d, 0x76, 0x0e, 0xbe,
	0x9c, 0x55, 0xe7, 0xe0, 0xd7, 0x61, 0xd0, 0xb5, 0x63, 0xef, 0xab, 0x9b, 0xa7, 0x2a, 0xf5, 0xf1,
	0xf3, 0xf6, 0xec, 0x4f, 0x00, 0x00, 0x00, 0xff, 0xff, 0x3e, 0xa5, 0x4f, 0xaf, 0xeb, 0x04, 0x00,
	0x00,
}

// Reference imports to suppress errors if they are not otherwise used.
var _ context.Context
var _ grpc.ClientConn

// This is a compile-time assertion to ensure that this generated file
// is compatible with the grpc package it is being compiled against.
const _ = grpc.SupportPackageIsVersion4

// DBAdminClient is the client API for DBAdmin service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://godoc.org/google.golang.org/grpc#ClientConn.NewStream.
type DBAdminClient interface {
	Stats(ctx context.Context, in *StatsRequest, opts ...grpc.CallOption) (*StatsReply, error)
	Tables(ctx context.Context, in *TablesRequest, opts ...grpc.CallOption) (*TablesReply, error)
	Exec(ctx context.Context, in *ExecRequest, opts ...grpc.CallOption) (*ExecReply, error)
	Query(ctx context.Context, in *QueryRequest, opts ...grpc.CallOption) (*QueryReply, error)
}

type dBAdminClient struct {
	cc *grpc.ClientConn
}

func NewDBAdminClient(cc *grpc.ClientConn) DBAdminClient {
	return &dBAdminClient{cc}
}

func (c *dBAdminClient) Stats(ctx context.Context, in *StatsRequest, opts ...grpc.CallOption) (*StatsReply, error) {
	out := new(StatsReply)
	err := c.cc.Invoke(ctx, "/com.gabecloud.sca.dbs.DBAdmin/Stats", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *dBAdminClient) Tables(ctx context.Context, in *TablesRequest, opts ...grpc.CallOption) (*TablesReply, error) {
	out := new(TablesReply)
	err := c.cc.Invoke(ctx, "/com.gabecloud.sca.dbs.DBAdmin/Tables", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *dBAdminClient) Exec(ctx context.Context, in *ExecRequest, opts ...grpc.CallOption) (*ExecReply, error) {
	out := new(ExecReply)
	err := c.cc.Invoke(ctx, "/com.gabecloud.sca.dbs.DBAdmin/Exec", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *dBAdminClient) Query(ctx context.Context, in *QueryRequest, opts ...grpc.CallOption) (*QueryReply, error) {
	out := new(QueryReply)
	err := c.cc.Invoke(ctx, "/com.gabecloud.sca.dbs.DBAdmin/Query", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// DBAdminServer is the server API for DBAdmin service.
type DBAdminServer interface {
	Stats(context.Context, *StatsRequest) (*StatsReply, error)
	Tables(context.Context, *TablesRequest) (*TablesReply, error)
	Exec(context.Context, *ExecRequest) (*ExecReply, error)
	Query(context.Context, *QueryRequest) (*QueryReply, error)
}

// UnimplementedDBAdminServer can be embedded to have forward compatible implementations.
type UnimplementedDBAdminServer struct {
}

func (*UnimplementedDBAdminServer) Stats(ctx context.Context, req *StatsRequest) (*StatsReply, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Stats not implemented")
}
func (*UnimplementedDBAdminServer) Tables(ctx context.Context, req *TablesRequest) (*TablesReply, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Tables not implemented")
}
func (*UnimplementedDBAdminServer) Exec(ctx context.Context, req *ExecRequest) (*ExecReply, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Exec not implemented")
}
func (*UnimplementedDBAdminServer) Query(ctx context.Context, req *QueryRequest) (*QueryReply, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Query not implemented")
}

func RegisterDBAdminServer(s *grpc.Server, srv DBAdminServer) {
	s.RegisterService(&_DBAdmin_serviceDesc, srv)
}

func _DBAdmin_Stats_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(StatsRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(DBAdminServer).Stats(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/com.gabecloud.sca.dbs.DBAdmin/Stats",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(DBAdminServer).Stats(ctx, req.(*StatsRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _DBAdmin_Tables_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(TablesRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(DBAdminServer).Tables(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/com.gabecloud.sca.dbs.DBAdmin/Tables",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(DBAdminServer).Tables(ctx, req.(*TablesRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _DBAdmin_Exec_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(ExecRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(DBAdminServer).Exec(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/com.gabecloud.sca.dbs.DBAdmin/Exec",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(DBAdminServer).Exec(ctx, req.(*ExecRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _DBAdmin_Query_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(QueryRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(DBAdminServer).Query(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/com.gabecloud.sca.dbs.DBAdmin/Query",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(DBAdminServer).Query(ctx, req.(*QueryRequest))
	}
	return interceptor(ctx, in, info, handler)
}

var _DBAdmin_serviceDesc = grpc.ServiceDesc{
	ServiceName: "com.gabecloud.sca.dbs.DBAdmin",
	HandlerType: (*DBAdminServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "Stats",
			Handler:    _DBAdmin_Stats_Handler,
		},
		{
			MethodName: "Tables",
			Handler:    _DBAdmin_Tables_Handler,
		},
		{
			MethodName: "Exec",
			Handler:    _DBAdmin_Exec_Handler,
		},
		{
			MethodName: "Query",
			Handler:    _DBAdmin_Query_Handler,
		},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "dbs.proto",
}
