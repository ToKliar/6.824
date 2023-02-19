package shardctrler

//
// Shard controler: assigns shards to replication groups.
//
// RPC interface:
// Join(servers) -- add a set of groups (gid -> server-list mapping).
// Leave(gids) -- delete a set of groups.
// Move(shard, gid) -- hand off one shard from current owner to gid.
// Query(num) -> fetch Config # num, or latest config if num==-1.
//
// A Config (configuration) describes a set of replica groups, and the
// replica group responsible for each shard. Configs are numbered. Config
// #0 is the initial configuration, with no groups and all shards
// assigned to group 0 (the invalid group).
//
// You will need to add fields to the RPC argument structs.
//

// The number of shards.
const NShards = 10

// A configuration -- an assignment of shards to groups.
// Please don't change this.
type Config struct {
	Num    int              // config number
	Shards [NShards]int     // shard -> gid
	Groups map[int][]string // gid -> servers[]
}

type Operation int

const (
	OpJoin Operation = iota
	OpLeave
	OpMove
	OpQuery
)

const (
	OK = "OK"
)

type Err string

type CommandArgs struct {
	Servers   map[int][]string
	GIDs      []int
	Shard     int
	GID       int
	Num       int
	Op        Operation
	ClientId  int64
	CommandId int
}

type CommandReply struct {
	WrongLeader bool
	Err         Err
	Config      Config
}

func DefaultConfig() Config {
	config := Config{}
	config.Groups = make(map[int][]string)
	config.Num = 0
	return config
}
