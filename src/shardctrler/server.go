package shardctrler

import (
	"bytes"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"6.824/labgob"
	"6.824/labrpc"
	"6.824/raft"
)

type ShardCtrler struct {
	mu      sync.RWMutex
	me      int
	rf      *raft.Raft
	applyCh chan raft.ApplyMsg
	dead    int32

	lastApplied        int
	notifyChans        map[int]chan *CommandReply
	lastOperation      map[int64]OpContext
	configStateMachine MemoryConfig
}

type OpContext struct {
	PrevCommandId int
	Result        *CommandReply
}

func (sc *ShardCtrler) isDuplicateCommand(clientId int64, commandId int) bool {
	if value, ok := sc.lastOperation[clientId]; ok {
		if value.PrevCommandId >= commandId {
			return true
		}
	}
	return false
}

func (sc *ShardCtrler) Command(args *CommandArgs, reply *CommandReply) {
	sc.mu.RLock()
	if args.Op != OpQuery && sc.isDuplicateCommand(args.ClientId, args.CommandId) {
		prevResult := sc.lastOperation[args.ClientId].Result
		reply.WrongLeader, reply.Config, reply.Err = prevResult.WrongLeader, prevResult.Config, prevResult.Err
		sc.mu.RUnlock()
		return
	}
	sc.mu.RUnlock()
	index, _, isLeader := sc.rf.Start(*args)
	if !isLeader {
		reply.WrongLeader = true
		return
	}

	sc.mu.Lock()
	ch, ok := sc.notifyChans[index]
	if !ok {
		sc.notifyChans[index] = make(chan *CommandReply, 1)
		ch = sc.notifyChans[index]
	}
	sc.mu.Unlock()
	select {
	case result := <-ch:
		reply.Config, reply.Err = result.Config, result.Err
	case <-time.After(time.Second * 2):
		reply.WrongLeader = true
	}

	go func() {
		sc.mu.Lock()
		if reply.Err == OK {
			close(sc.notifyChans[index])
			delete(sc.notifyChans, index)
		}
		sc.mu.Unlock()
	}()
}

func (sc *ShardCtrler) needSnapshot() bool {
	return false
}

func (sc *ShardCtrler) takeSnapshot(commandIndex int) {
	w := new(bytes.Buffer)
	e := labgob.NewEncoder(w)
	e.Encode(sc.lastApplied)
	e.Encode(sc.configStateMachine.Configs)
	e.Encode(sc.lastOperation)
	sc.rf.ReplaceLogSnapshot(commandIndex, w.Bytes())
}

func (sc *ShardCtrler) restoreSnapshot(snapshot []byte) {
	r := bytes.NewBuffer(snapshot)
	d := labgob.NewDecoder(r)
	var lastApplied int
	var configs []Config
	var lastOperation map[int64]OpContext
	if d.Decode(&lastApplied) == nil && d.Decode(&configs) == nil && d.Decode(&lastOperation) == nil {
		sc.lastApplied = lastApplied
		sc.lastOperation = lastOperation
		sc.configStateMachine = MemoryConfig{configs}
	}
}

func (sc *ShardCtrler) applier() {
	for sc.killed() == false {
		select {
		case message := <-sc.applyCh:
			if message.CommandValid {
				sc.mu.Lock()
				if message.CommandIndex <= sc.lastApplied {
					sc.mu.Unlock()
					continue
				}
				sc.lastApplied = message.CommandIndex

				var reply *CommandReply
				command := message.Command.(CommandArgs)
				if command.Op != OpQuery && sc.isDuplicateCommand(command.ClientId, command.CommandId) {
					reply = sc.lastOperation[command.ClientId].Result
				} else {
					reply = sc.applyLogToStateMachine(command)
					if command.Op != OpQuery {
						sc.lastOperation[command.ClientId] = OpContext{command.CommandId, reply}
					}
				}

				currentTerm, isLeader := sc.rf.GetState()
				if isLeader && message.CommandTerm == currentTerm {
					ch, ok := sc.notifyChans[message.CommandIndex]
					if !ok {
						sc.notifyChans[message.CommandIndex] = make(chan *CommandReply, 1)
						ch = sc.notifyChans[message.CommandIndex]
					}
					ch <- reply
				}
				sc.mu.Unlock()
			} else if message.SnapshotValid {
				sc.mu.Lock()
				if sc.rf.CondInstallSnapshot(message.SnapshotTerm, message.SnapshotIndex, message.Snapshot) {
					sc.restoreSnapshot(message.Snapshot)
					sc.lastApplied = message.SnapshotIndex
				}
				sc.mu.Unlock()
			} else {
				panic(fmt.Sprintf("unexpected message: %v", message))
			}
		}
	}
}

func (sc *ShardCtrler) applyLogToStateMachine(command CommandArgs) *CommandReply {
	reply := new(CommandReply)
	switch command.Op {
	case OpQuery:
		reply.Config, reply.Err = sc.configStateMachine.Query(command.Num)
	case OpJoin:
		reply.Err = sc.configStateMachine.Join(command.Servers)
	case OpLeave:
		reply.Err = sc.configStateMachine.Leave(command.GIDs)
	case OpMove:
		reply.Err = sc.configStateMachine.Move(command.Shard, command.GID)
	default:
		panic(fmt.Sprintf("unexpected optype: %v", command))
	}
	return reply
}

//
// the tester calls Kill() when a ShardCtrler instance won't
// be needed again. you are not required to do anything
// in Kill(), but it might be convenient to (for example)
// turn off debug output from this instance.
//
func (sc *ShardCtrler) Kill() {
	atomic.StoreInt32(&sc.dead, 1)
	sc.rf.Kill()
}

func (sc *ShardCtrler) killed() bool {
	z := atomic.LoadInt32(&sc.dead)
	return z == 1
}

// needed by shardsc tester
func (sc *ShardCtrler) Raft() *raft.Raft {
	return sc.rf
}

//
// servers[] contains the ports of the set of
// servers that will cooperate via Raft to
// form the fault-tolerant shardctrler service.
// me is the index of the current server in servers[].
//
func StartServer(servers []*labrpc.ClientEnd, me int, persister *raft.Persister) *ShardCtrler {
	sc := new(ShardCtrler)
	sc.me = me

	labgob.Register(CommandArgs{})
	sc.applyCh = make(chan raft.ApplyMsg)
	sc.rf = raft.Make(servers, me, persister, sc.applyCh)

	// Your code here.
	sc.configStateMachine = *NewMemoryConfig()
	sc.notifyChans = make(map[int]chan *CommandReply)
	sc.lastOperation = make(map[int64]OpContext)
	sc.lastApplied = 0

	go sc.applier()
	return sc
}
