package shardctrler

//
// Shardctrler clerk.
//

import "6.824/labrpc"
import "time"
import "crypto/rand"
import "math/big"

type Clerk struct {
	servers 	[]*labrpc.ClientEnd
	clientId	int64
	commandId	int
}

func nrand() int64 {
	max := big.NewInt(int64(1) << 62)
	bigx, _ := rand.Int(rand.Reader, max)
	x := bigx.Int64()
	return x
}

func MakeClerk(servers []*labrpc.ClientEnd) *Clerk {
	ck := new(Clerk)
	ck.servers = servers
	// Your code here.
	ck.clientId = nrand()
	ck.commandId = 0
	return ck
}

func (ck *Clerk) Command(args *CommandArgs) Config {
	args.ClientId, args.CommandId = ck.clientId, ck.commandId
	for {
		for _, srv := range ck.servers {
			var reply CommandReply 
			ok := srv.Call("ShardCtrler.Command", args, &reply)
			if ok && reply.WrongLeader == false {
				ck.commandId++
				return reply.Config
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (ck *Clerk) Query(num int) Config {
	args := &CommandArgs{Num: num, Op: OpQuery}
	return ck.Command(args);
}

func (ck *Clerk) Join(servers map[int][]string) {
	args := &CommandArgs{Servers: servers, Op: OpJoin}
	ck.Command(args)
}

func (ck *Clerk) Leave(gids []int) {
	args := &CommandArgs{GIDs: gids, Op: OpLeave}
	ck.Command(args)
}

func (ck *Clerk) Move(shard int, gid int) {
	args := &CommandArgs{Shard: shard, GID: gid, Op: OpMove}
	ck.Command(args)
}
