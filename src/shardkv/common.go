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

const ConfigureMonitorTimeout = 50 * time.Millisecond
const MigrationMonitorTimeout = 80 * time.Millisecond
const GCMonitorTimeout = 100 * time.Millisecond
const EmptyEntryMonitorTimeout = 100 * time.Millisecond

const (
	OK             	= "OK"
	ErrNoKey       	= "ErrNoKey"
	ErrWrongGroup  	= "ErrWrongGroup"
	ErrWrongLeader 	= "ErrWrongLeader"
	ErrOutDated	   	= "ErrOutDated"
	ErrNotReady	   	= "ErrNotReady"
	ErrTimeout 		= "ErrTimeout"
)

type OperationType int 

const (
	OpGet OperationType	= iota	
	OpPut		
	OpAppend	
)

type Err string

// Put or Append
type PutAppendArgs struct {
	// You'll have to add definitions here.
	Key   string
	Value string
	Op    string // "Put" or "Append"
	// You'll have to add definitions here.
	// Field names must start with capital letters,
	// otherwise RPC will break.
}

type PutAppendReply struct {
	Err Err
}

type GetArgs struct {
	Key string
	// You'll have to add definitions here.
}

type GetReply struct {
	Err   Err
	Value string
}

type CommandArgs struct {
	Key 		string
	Value 		string
	Op 			OperationType
	CommandId 	int
	ClientId	int64
}

type CommandReply struct {
	Err		Err
	Value	string	
}

type ShardOperationArgs struct {
	ConfigNum	int
	ShardIDs	[]int	
}

type ShardOperationReply struct {
	Err				Err
	Shards			map[int]map[string]string
	LastOperation	map[int64]OpContext
	ConfigNum		int
}