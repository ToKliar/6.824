package kvraft

import (
	"6.824/labgob"
	"6.824/labrpc"
	"6.824/raft"
	"log"
	"sync"
	"sync/atomic"
	"os"
	"strconv"
	"time"
	"fmt"
)

var Debug bool

func init() {
	verbosity := getVerbosity()
	if verbosity == 2 {
		Debug = true
	} else {
		Debug = false
	}
}


func DPrintf(format string, a ...interface{}) (n int, err error) {
	if Debug {
		log.Printf(format, a...)
	}
	return
}

func getVerbosity() int {
	v := os.Getenv("VERBOSE")
	level := 0
	if v != "" {
		var err error
		level, err = strconv.Atoi(v)
		if err != nil {
			log.Fatalf("Invalid verbosity %v", v)
		}
	}
	return level
}

type KVServer struct {
	mu      sync.RWMutex
	me      int
	rf      *raft.Raft
	applyCh chan raft.ApplyMsg
	dead    int32 // set by Kill()

	maxraftstate int // snapshot if log grows this big
	lastApplied	 int

	stateMachine 	MemoryKV
	notifyChans		map[int]chan *CommandReply
	lastOperation	map[int64]OpContext
}

type OpContext struct {
	prevCommandId	int
	result			*CommandReply
}

func (kv *KVServer) isDuplicateCommand(clientId int64, commandId int) bool {
	if value, ok := kv.lastOperation[clientId]; ok {
		if value.prevCommandId >= commandId {
			return true
		}
	}
	return false
}

func (kv *KVServer) Command(args *CommandArgs, reply *CommandReply) {
	DPrintf("KVS%d Receive Command From Client:%d Op:%s CommandId:%d", kv.me, args.ClientId, args.Op, args.CommandId);
	kv.mu.RLock()
	if args.Op != OpGet && kv.isDuplicateCommand(args.ClientId, args.CommandId) {
		prevResult := kv.lastOperation[args.ClientId].result
		reply.Value, reply.Err = prevResult.Value, prevResult.Err
		kv.mu.RUnlock()
		return
	}
	kv.mu.RUnlock()
	
	index, _, isLeader := kv.rf.Start(*args)
	if !isLeader {
		DPrintf("KVS%d Not Leader", kv.me);
		reply.Err = ErrWrongLeader
		return 
	}
	
	kv.mu.Lock()
	ch, ok := kv.notifyChans[index]
	if !ok {
		kv.notifyChans[index] = make(chan *CommandReply)
		ch = kv.notifyChans[index]
	}
	kv.mu.Unlock()
	select {
	case result := <-ch:
		DPrintf("KVS%d Get Reply %v In Command RPC", kv.me, result)
		reply.Value, reply.Err = result.Value, result.Err
	case <-time.After(time.Second * 2):
		DPrintf("KVS%d Get Reply Timeout In Command RPC", kv.me)
		reply.Err = ErrWrongLeader
	}

	// go func() {
	// 	kv.mu.Lock()
	// 	if reply.Err == OK {
	// 		close(kv.notifyChans[index])
	// 		delete(kv.notifyChans, index)
	// 	}
	// 	kv.mu.Unlock()
	// }()
}

func (kv *KVServer) applier() {
	for kv.killed() == false {
		select {
		case message := <-kv.applyCh:
			if message.CommandValid {
				kv.mu.Lock()
				DPrintf("KVS%d Get Message CommandTerm:%d CommandIndex:%d", kv.me, message.CommandTerm, message.CommandIndex)
				if message.CommandIndex <= kv.lastApplied {
					DPrintf("KVS%d discards outdated message %v because a newer snapshot which lastApplied is %v has been restored", kv.me, message, kv.lastApplied)
					kv.mu.Unlock()
					continue
				}
				kv.lastApplied = message.CommandIndex

				var reply *CommandReply
				command := message.Command.(CommandArgs)
				if command.Op != OpGet && kv.isDuplicateCommand(command.ClientId, command.CommandId) {
					DPrintf("KVS%d By Client:%d Doesn't Apply Duplicate Command:%d, Last Command:%d", kv.me, 
						command.ClientId,command.CommandId, kv.lastOperation[command.ClientId].prevCommandId)
					reply = kv.lastOperation[command.ClientId].result
				} else {
					DPrintf("KVS%d By Client:%d Apply Newest Command:%d, Op:%s, Key:%s, Value:%s", kv.me, 
						command.ClientId, command.CommandId, command.Op, command.Key, command.Value)
					reply = kv.applyLogToStateMachine(command) 
					if command.Op != OpGet {
						kv.lastOperation[command.ClientId] = OpContext { command.CommandId, reply}
					}
				}
				
				currentTerm, isLeader := kv.rf.GetState()
				DPrintf("KVS%d Raft State CurrentTerm:%d isLeader:%t", kv.me, currentTerm, isLeader)
				if isLeader && message.CommandTerm == currentTerm {
					DPrintf("KVS%d Send Reply %v to Channel Index:%d From Arg %v", kv.me, reply, message.CommandIndex, command)
					ch := kv.notifyChans[message.CommandIndex]
					ch <- reply
				}
				kv.mu.Unlock()
			} else if message.SnapshotValid {

			} else {
				panic(fmt.Sprintf("unexpected message: %v", message))
			}
		}
	}
}

func (kv *KVServer) applyLogToStateMachine(command CommandArgs) *CommandReply{
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
// the tester calls Kill() when a KVServer instance won't
// be needed again. for your convenience, we supply
// code to set rf.dead (without needing a lock),
// and a killed() method to test rf.dead in
// long-running loops. you can also add your own
// code to Kill(). you're not required to do anything
// about this, but it may be convenient (for example)
// to suppress debug output from a Kill()ed instance.
//
func (kv *KVServer) Kill() {
	atomic.StoreInt32(&kv.dead, 1)
	kv.rf.Kill()
	// Your code here, if desired.
}

func (kv *KVServer) killed() bool {
	z := atomic.LoadInt32(&kv.dead)
	return z == 1
}

//
// servers[] contains the ports of the set of
// servers that will cooperate via Raft to
// form the fault-tolerant key/value service.
// me is the index of the current server in servers[].
// the k/v server should store snapshots through the underlying Raft
// implementation, which should call persister.SaveStateAndSnapshot() to
// atomically save the Raft state along with the snapshot.
// the k/v server should snapshot when Raft's saved state exceeds maxraftstate bytes,
// in order to allow Raft to garbage-collect its log. if maxraftstate is -1,
// you don't need to snapshot.
// StartKVServer() must return quickly, so it should start goroutines
// for any long-running work.
//
func StartKVServer(servers []*labrpc.ClientEnd, me int, persister *raft.Persister, maxraftstate int) *KVServer {
	// call labgob.Register on structures you want
	// Go's RPC library to marshall/unmarshall.
	labgob.Register(CommandArgs{})

	kv := new(KVServer)
	kv.me = me
	kv.maxraftstate = maxraftstate

	// You may need initialization code here.

	kv.applyCh = make(chan raft.ApplyMsg)
	kv.rf = raft.Make(servers, me, persister, kv.applyCh)
	kv.stateMachine = *NewMemoryKV()
	kv.notifyChans = make(map[int]chan *CommandReply)
	kv.lastOperation = make(map[int64]OpContext)
	kv.lastApplied = 0

	go kv.applier()
	return kv
}
