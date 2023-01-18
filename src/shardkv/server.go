package shardkv


import "6.824/labrpc"
import "6.824/raft"
import "sync"
import "6.824/labgob"
import "sync/atomic"
import "time"
import "fmt"
import "bytes"


type ShardKV struct {
	mu           sync.RWMutex
	me           int
	rf           *raft.Raft
	applyCh      chan raft.ApplyMsg
	make_end     func(string) *labrpc.ClientEnd
	gid          int
	ctrlers      []*labrpc.ClientEnd
	maxRaftState int // snapshot if log grows this big
	dead		 int32

	lastApplied	 int

	stateMachine 	MemoryKV
	notifyChans		map[int]chan *CommandReply
	lastOperation	map[int64]OpContext
}

type OpContext struct {
	PrevCommandId	int
	Result			*CommandReply
}

func (kv *ShardKV) isDuplicateCommand(clientId int64, commandId int) bool {
	if value, ok := kv.lastOperation[clientId]; ok {
		if value.PrevCommandId >= commandId {
			return true
		}
	}
	return false
}

func (kv *ShardKV) Command(args *CommandArgs, reply *CommandReply) {
	kv.mu.RLock()
	if args.Op != OpGet && kv.isDuplicateCommand(args.ClientId, args.CommandId) {
		prevResult := kv.lastOperation[args.ClientId].Result
		reply.Value, reply.Err = prevResult.Value, prevResult.Err
		kv.mu.RUnlock()
		return
	}
	kv.mu.RUnlock()
	
	index, _, isLeader := kv.rf.Start(*args)
	if !isLeader {
		reply.Err = ErrWrongLeader
		return 
	}
	
	kv.mu.Lock()
	ch, ok := kv.notifyChans[index]
	if !ok {
		kv.notifyChans[index] = make(chan *CommandReply, 1)
		ch = kv.notifyChans[index]
	}
	kv.mu.Unlock()
	select {
	case result := <-ch:
		reply.Value, reply.Err = result.Value, result.Err
	case <-time.After(time.Second * 2):
		reply.Err = ErrWrongLeader
	}

	go func() {
		kv.mu.Lock()
		if reply.Err == OK {
			close(kv.notifyChans[index])
			delete(kv.notifyChans, index)
		}
		kv.mu.Unlock()
	}()
}

func (kv *ShardKV) needSnapshot() bool {
	if kv.maxRaftState == -1 {
		return false
	}
	return kv.rf.GetRaftStateSize() >= kv.maxRaftState
}

func (kv *ShardKV) takeSnapshot(commandIndex int) {
	w := new(bytes.Buffer)
	e := labgob.NewEncoder(w)
	e.Encode(kv.lastApplied)
	e.Encode(kv.stateMachine.KV)
	e.Encode(kv.lastOperation)
	kv.rf.ReplaceLogSnapshot(commandIndex, w.Bytes())
}

func (kv *ShardKV) restoreSnapshot(snapshot []byte) {
	r := bytes.NewBuffer(snapshot)
	d := labgob.NewDecoder(r)
	var lastApplied int
	var kvStore map[string]string
	var lastOperation map[int64]OpContext
	if d.Decode(&lastApplied) == nil && d.Decode(&kvStore) == nil && d.Decode(&lastOperation) == nil {
		kv.lastApplied = lastApplied
		kv.lastOperation = lastOperation
		kv.stateMachine = MemoryKV{kvStore}
	} 
}

func (kv *ShardKV) applier() {
	for kv.killed() == false {
		select {
		case message := <-kv.applyCh:
			if message.CommandValid {
				kv.mu.Lock()
				if message.CommandIndex <= kv.lastApplied {
					kv.mu.Unlock()
					continue
				}
				kv.lastApplied = message.CommandIndex

				var reply *CommandReply
				command := message.Command.(CommandArgs)
				if command.Op != OpGet && kv.isDuplicateCommand(command.ClientId, command.CommandId) {
					reply = kv.lastOperation[command.ClientId].Result
				} else {
					reply = kv.applyLogToStateMachine(command) 
					if command.Op != OpGet {
						kv.lastOperation[command.ClientId] = OpContext { command.CommandId, reply}
					}
				}
				
				currentTerm, isLeader := kv.rf.GetState()
				if isLeader && message.CommandTerm == currentTerm {
					ch, ok := kv.notifyChans[message.CommandIndex]
					if !ok {
						kv.notifyChans[message.CommandIndex] = make(chan *CommandReply, 1)
						ch = kv.notifyChans[message.CommandIndex]
					}
					ch <- reply
				}

				needSnapshot := kv.needSnapshot()
				if needSnapshot {
					kv.takeSnapshot(message.CommandIndex)
				}
				kv.mu.Unlock()
			} else if message.SnapshotValid {
				kv.mu.Lock()
				if kv.rf.CondInstallSnapshot(message.SnapshotTerm, message.SnapshotIndex, message.Snapshot) {
					kv.restoreSnapshot(message.Snapshot)
					kv.lastApplied = message.SnapshotIndex
				}
				kv.mu.Unlock()
			} else {
				panic(fmt.Sprintf("unexpected message: %v", message))
			}
		}
	}
}

func (kv *ShardKV) applyLogToStateMachine(command CommandArgs) *CommandReply{
	reply := new(CommandReply)
	if command.Op == OpGet {
		reply.Value, reply.Err = kv.stateMachine.Get(command.Key)
	} else if command.Op == OpAppend {
		reply.Err = kv.stateMachine.Append(command.Key, command.Value)
	} else if command.Op == OpPut {
		reply.Err = kv.stateMachine.Put(command.Key, command.Value)
	}
	return reply
}

//
// the tester calls Kill() when a ShardKV instance won't
// be needed again. you are not required to do anything
// in Kill(), but it might be convenient to (for example)
// turn off debug output from this instance.
//
func (kv *ShardKV) Kill() {
	atomic.StoreInt32(&kv.dead, 1)
	kv.rf.Kill()
	// Your code here, if desired.
}

func (kv *ShardKV) killed() bool {
	z := atomic.LoadInt32(&kv.dead)
	return z == 1
}

//
// servers[] contains the ports of the servers in this group.
//
// me is the index of the current server in servers[].
//
// the k/v server should store snapshots through the underlying Raft
// implementation, which should call persister.SaveStateAndSnapshot() to
// atomically save the Raft state along with the snapshot.
//
// the k/v server should snapshot when Raft's saved state exceeds
// maxraftstate bytes, in order to allow Raft to garbage-collect its
// log. if maxraftstate is -1, you don't need to snapshot.
//
// gid is this group's GID, for interacting with the shardctrler.
//
// pass ctrlers[] to shardctrler.MakeClerk() so you can send
// RPCs to the shardctrler.
//
// make_end(servername) turns a server name from a
// Config.Groups[gid][i] into a labrpc.ClientEnd on which you can
// send RPCs. You'll need this to send RPCs to other groups.
//
// look at client.go for examples of how to use ctrlers[]
// and make_end() to send RPCs to the group owning a specific shard.
//
// StartServer() must return quickly, so it should start goroutines
// for any long-running work.
//
func StartServer(servers []*labrpc.ClientEnd, me int, persister *raft.Persister, maxraftstate int, gid int, ctrlers []*labrpc.ClientEnd, make_end func(string) *labrpc.ClientEnd) *ShardKV {
	// call labgob.Register on structures you want
	// Go's RPC library to marshall/unmarshall.
	labgob.Register(CommandArgs{})

	kv := new(ShardKV)
	kv.me = me
	kv.maxRaftState = maxraftstate
	kv.make_end = make_end
	kv.gid = gid
	kv.ctrlers = ctrlers

	// Your initialization code here.

	// Use something like this to talk to the shardctrler:
	// kv.mck = shardctrler.MakeClerk(kv.ctrlers)

	kv.applyCh = make(chan raft.ApplyMsg)
	kv.rf = raft.Make(servers, me, persister, kv.applyCh)

	kv.lastApplied = 0
	kv.stateMachine = *NewMemoryKV()
	kv.notifyChans = make(map[int]chan *CommandReply)
	kv.lastOperation = make(map[int64]OpContext)

	snapshot := persister.ReadSnapshot()
	kv.restoreSnapshot(snapshot)

	go kv.applier()
	return kv
}
