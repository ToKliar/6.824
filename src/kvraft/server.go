package kvraft

import (
	"6.824/labgob"
	"6.824/labrpc"
	"6.824/raft"
	"log"
	"sync"
	"sync/atomic"
	"time"
)

const Debug = false

func DPrintf(format string, a ...interface{}) (n int, err error) {
	if Debug {
		log.Printf(format, a...)
	}
	return
}


type Op struct {
	// Your definitions here.
	// Field names must start with capital letters,
	// otherwise RPC will break.
}

type KVServer struct {
	mu      sync.RWMutex
	me      int
	rf      *raft.Raft
	applyCh chan raft.ApplyMsg
	dead    int32 // set by Kill()

	maxraftstate int // snapshot if log grows this big

	stateMachine 	KVStateMachine
	notifyChans		map[int]chan *CommandReply
}

func (kv *KVServer) Command(args *CommandArgs, reply *CommandReply) {
	kv.mu.RLock()
	index, _, is_leader := kv.rf.Start(args)
	if !is_leader {
		reply.Err = ErrWrongLeader
		kv.mu.RUnlock()
		return 
	}
	kv.mu.RUnlock()
	kv.mu.Lock()
	ch := kv.notifyChans[index]
	kv.mu.Unlock()
	select {
	case result := <-ch:
		reply.Value, reply.Err = result.Value, result.Err
	case <-time.After(ExecuteTimeout):
		reply.Err = ErrTimeout
	}
}

func (kv *KVServer) applier() {
	for kv.killed() == false {
		select {
		case message := <-kv.applyCh:
			kv.mu.Lock()
			var reply *CommandReply
			command := message.Command.(*CommandArgs)
			reply = kv.applyLogToStateMachine(command) 

			if currentTerm, isLeader := kv.rf.GetState(); isLeader && message.CommandTerm == currentTerm {
				ch := kv.notifyChans[message.CommandIndex]
				ch <- reply
			}
			kv.mu.Unlock()
		}
	}
}

func (kv *KVServer) applyLogToStateMachine(command *CommandArgs) *CommandReply{
	var reply *CommandReply
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
	kv.stateMachine = MakeMemoryKV()

	// You may need initialization code here.

	return kv
}
