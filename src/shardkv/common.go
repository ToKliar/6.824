package shardkv

import "time"

//
// Sharded key/value server.
// Lots of replica groups, each running Raft.
// Shardctrler decides which group serves each shard.
// Shardctrler may change shard assignment from time to time.
//
// You will have to modify these definitions.
//

const ConfigureMonitorTimeout = 100 * time.Millisecond
const MigrationMonitorTimeout = 100 * time.Millisecond
const GCMonitorTimeout = 100 * time.Millisecond
const EmptyEntryMonitorTimeout = 500 * time.Millisecond

const (
	OK             = "OK"
	ErrNoKey       = "ErrNoKey"
	ErrWrongGroup  = "ErrWrongGroup"
	ErrWrongLeader = "ErrWrongLeader"
	ErrOutDated    = "ErrOutDated"
	ErrNotReady    = "ErrNotReady"
	ErrTimeout     = "ErrTimeout"
)

type OperationType int

const (
	OpGet OperationType = iota
	OpPut
	OpAppend
)

type Err string

type CommandArgs struct {
	Key       string
	Value     string
	Op        OperationType
	CommandId int
	ClientId  int64
}

type CommandReply struct {
	Err   Err
	Value string
}

type ShardOperationArgs struct {
	ConfigNum int
	ShardIDs  []int
}

type ShardOperationReply struct {
	Err           Err
	Shards        map[int]map[string]string
	LastOperation map[int64]OpContext
	ConfigNum     int
}
