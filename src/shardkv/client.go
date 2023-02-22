package shardkv

//
// client code to talk to a sharded key/value service.
//
// the client first talks to the shardctrler to find out
// the assignment of shards (keys) to groups, and then
// talks to the group that holds the key's shard.
//

import (
	"crypto/rand"
	"math/big"
	"time"

	"6.824/labrpc"
	"6.824/shardctrler"
)

//
// which shard is a key in?
// please use this function,
// and please do not change it.
//
func key2shard(key string) int {
	shard := 0
	if len(key) > 0 {
		shard = int(key[0])
	}
	shard %= shardctrler.NShards
	return shard
}

func nrand() int64 {
	max := big.NewInt(int64(1) << 62)
	bigx, _ := rand.Int(rand.Reader, max)
	x := bigx.Int64()
	return x
}

type Clerk struct {
	sm       *shardctrler.Clerk
	config   shardctrler.Config
	make_end func(string) *labrpc.ClientEnd
	// You will have to modify this struct.
	clientId  int64
	leaderIds map[int]int
	commandId int
}

//
// the tester calls MakeClerk.
//
// ctrlers[] is needed to call shardctrler.MakeClerk().
//
// make_end(servername) turns a server name from a
// Config.Groups[gid][i] into a labrpc.ClientEnd on which you can
// send RPCs.
//
func MakeClerk(ctrlers []*labrpc.ClientEnd, make_end func(string) *labrpc.ClientEnd) *Clerk {
	ck := new(Clerk)
	ck.sm = shardctrler.MakeClerk(ctrlers)
	ck.make_end = make_end
	ck.config = ck.sm.Query(-1)
	ck.clientId = nrand()
	ck.commandId = 0
	ck.leaderIds = make(map[int]int)
	return ck
}

func (ck *Clerk) Command(args *CommandArgs) string {
	args.ClientId, args.CommandId = ck.clientId, ck.commandId
	for {
		shard := key2shard(args.Key)
		gid := ck.config.Shards[shard]
		if servers, ok := ck.config.Groups[gid]; ok {
			if _, ok = ck.leaderIds[gid]; !ok {
				ck.leaderIds[gid] = 0
			}
			oldLeaderId := ck.leaderIds[gid]
			newLeaderId := oldLeaderId
			for {
				var reply CommandReply
				ok := ck.make_end(servers[newLeaderId]).Call("ShardKV.Command", args, &reply)
				if ok && (reply.Err == OK || reply.Err == ErrNoKey) {
					ck.commandId++
					ck.leaderIds[gid] = newLeaderId
					return reply.Value
				} else if ok && reply.Err == ErrWrongGroup {
					break
				} else {
					newLeaderId = (newLeaderId + 1) % len(servers)
					if newLeaderId == oldLeaderId {
						break
					}
					continue
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
		// 获取最新配置
		ck.config = ck.sm.Query(-1)
	}
}

func (ck *Clerk) Get(key string) string {
	return ck.Command(&CommandArgs{Key: key, Op: OpGet})
}

func (ck *Clerk) Put(key string, value string) {
	ck.Command(&CommandArgs{Key: key, Value: value, Op: OpPut})
}
func (ck *Clerk) Append(key string, value string) {
	ck.Command(&CommandArgs{Key: key, Value: value, Op: OpAppend})
}
