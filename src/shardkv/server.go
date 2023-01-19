package shardkv


import "6.824/labrpc"
import "6.824/raft"
import "sync"
import "6.824/labgob"
import "sync/atomic"
import "time"
import "fmt"
import "bytes"
import "6.824/shardctrler"


type ShardKV struct {
	mu           sync.RWMutex
	me           int
	rf           *raft.Raft
	applyCh      chan raft.ApplyMsg
	make_end     func(string) *labrpc.ClientEnd
	gid          int
	sc  		 *shardctrler.Clerk
	
	maxRaftState int // snapshot if log grows this big
	dead		 int32
	lastApplied	 int

	lastConfig		shardctrler.Config
	currentConfig	shardctrler.Config
	stateMachine 	map[int]*Shard
	notifyChans		map[int]chan *CommandReply
	lastOperation	map[int64]OpContext
}

type OpContext struct {
	PrevCommandId	int
	Result			*CommandReply
}

func (oc *OpContext) deepCopy() OpContext {
	result := &CommandReply{oc.Result.Err, oc.Result.Value}
	return OpContext{oc.PrevCommandId, result}
}

func (kv *ShardKV) isDuplicateCommand(clientId int64, commandId int) bool {
	if value, ok := kv.lastOperation[clientId]; ok {
		if value.PrevCommandId >= commandId {
			return true
		}
	}
	return false
}

func (kv *ShardKV) canServe(shardID int) bool {
	return kv.currentConfig.Shards[shardID] == kv.gid && (kv.stateMachine[shardID].Status == Serving || kv.stateMachine[shardID].Status == GCing)
}

func (kv *ShardKV) Command(args *CommandArgs, reply *CommandReply) {
	kv.mu.RLock()
	if args.Op != OpGet && kv.isDuplicateCommand(args.ClientId, args.CommandId) {
		prevResult := kv.lastOperation[args.ClientId].Result
		reply.Value, reply.Err = prevResult.Value, prevResult.Err
		kv.mu.RUnlock()
		return
	}

	if !kv.canServe(key2shard(args.Key)) {
		reply.Err = ErrWrongGroup
		kv.mu.RUnlock()
		return
	}

	kv.mu.RUnlock()

	kv.Execute(NewOperationCommand(args), reply)
}

func (kv *ShardKV) Execute(command Command, reply *CommandReply) {
	index, _, isLeader := kv.rf.Start(command)
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
		reply.Err = ErrTimeout
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
	e.Encode(kv.stateMachine)
	e.Encode(kv.lastOperation)
	e.Encode(kv.lastConfig)
	e.Encode(kv.currentConfig)
	kv.rf.ReplaceLogSnapshot(commandIndex, w.Bytes())
}

func (kv *ShardKV) restoreSnapshot(snapshot []byte) {
	r := bytes.NewBuffer(snapshot)
	d := labgob.NewDecoder(r)
	var lastApplied int
	var stateMachine map[int]*Shard
	var lastOperation map[int64]OpContext
	var lastConfig shardctrler.Config
	var currentConfig shardctrler.Config
	if d.Decode(&lastApplied) != nil {
		return
	} 
	if d.Decode(&stateMachine) != nil {
		return
	} 
	if d.Decode(&lastOperation) != nil {
		return		
	} 
	if d.Decode(&lastConfig) != nil {
		return
	}
	if d.Decode(&currentConfig) != nil {
		return
	}

	kv.lastApplied = lastApplied
	kv.lastOperation = lastOperation
	kv.stateMachine = stateMachine
	kv.lastConfig = lastConfig
	kv.currentConfig = currentConfig
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

				command := message.Command.(Command)
				switch command.Op {
				case Operation:
					operation := command.Data.(CommandArgs)
					reply = kv.applyOperation(&message, &operation)
				case Configuration:
					nextConfig := command.Data.(shardctrler.Config)
					reply = kv.applyConfiguration(&nextConfig)
				case InsertShards:
					shardsInfo := command.Data.(ShardOperationReply)
					reply = kv.applyInsertShards(&shardsInfo)
				case DeleteShards:
					shardsInfo := command.Data.(ShardOperationArgs)
					reply = kv.applyDeleteShards(&shardsInfo)
				case EmptyEntry:
					reply = kv.applyEmptyEntry()
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

func (kv *ShardKV) applyOperation(message *raft.ApplyMsg, operation *CommandArgs) *CommandReply {
	var reply *CommandReply
	shardID := key2shard(operation.Key)
	if kv.canServe(shardID) {
		if operation.Op != OpGet && kv.isDuplicateCommand(operation.ClientId, operation.CommandId) {
			return kv.lastOperation[operation.ClientId].Result
		} else {
			reply = kv.applyLogToStateMachine(operation, shardID) 
			if operation.Op != OpGet {
				kv.lastOperation[operation.ClientId] = OpContext { operation.CommandId, reply}
			}
			return reply
		}
	}
	return &CommandReply{ErrWrongGroup, ""}
}

func (kv *ShardKV) applyLogToStateMachine(operation *CommandArgs, shardID int) *CommandReply{
	reply := new(CommandReply)
	shard, ok := kv.stateMachine[shardID]
	if !ok {
		reply.Err = ErrWrongLeader
	} else {
		switch operation.Op {
		case OpGet:
			reply.Value, reply.Err = shard.Get(operation.Key)
		case OpAppend:
			reply.Err = shard.Append(operation.Key, operation.Value)
		case OpPut:
			reply.Err = shard.Put(operation.Key, operation.Value)
		}
	} 
	return reply
}

func (kv *ShardKV) updateShardStatus (nextConfig *shardctrler.Config) {
	for shardID, shard := range kv.stateMachine {
		if nextConfig.Shards[shardID] != kv.gid {
			shard.Status = BePulling
		}
	}

	for shardID, gid := range nextConfig.Shards {
		if gid == kv.gid {
			if _, ok := kv.stateMachine[shardID]; !ok {
				kv.stateMachine[shardID] = NewShard()
			}
			kv.stateMachine[shardID].Status = Pulling
		}
	}
}

func (kv *ShardKV) configureAction() {
	canPerformNextConfig := true
	kv.mu.RLock()
	for _, shard := range kv.stateMachine {
		if shard.Status != Serving {
			canPerformNextConfig = false
			break
		}
	}
	currentConfigNum := kv.currentConfig.Num
	kv.mu.RLock()

	if canPerformNextConfig {
		nextConfig := kv.sc.Query(currentConfigNum + 1)
		if nextConfig.Num == currentConfigNum + 1 {
			kv.Execute(NewConfigurationCommand(&nextConfig), &CommandReply{})
		}
	}
}

func (kv *ShardKV) applyConfiguration(nextConfig *shardctrler.Config) *CommandReply{
	if nextConfig.Num == kv.currentConfig.Num + 1 {
		kv.updateShardStatus(nextConfig)
		kv.lastConfig = kv.currentConfig
		kv.currentConfig = *nextConfig
		return &CommandReply{OK, ""}
	}
	return &CommandReply{ErrOutDated, ""}
}

func (kv *ShardKV) migrationAction() {
	kv.mu.RLock()
	gid2ShardIDS := kv.getShardIDsByStatus(Pulling)
	var wg sync.WaitGroup
	for gid, shardIDs := range gid2ShardIDS {
		wg.Add(1)
		go func(servers[]string, configNum int, shardIDs []int) {
			defer wg.Done()
			pullTaskRequest := ShardOperationArgs{configNum, shardIDs}
			for _, server := range servers {
				var pullTaskReply ShardOperationReply
				srv := kv.make_end(server)
				if srv.Call("ShardKV.GetShardsData", &pullTaskRequest, &pullTaskReply) && pullTaskReply.Err == OK {
					kv.Execute(NewInsertShardsCommand(&pullTaskReply), &CommandReply{})
				} 
			}
		}(kv.lastConfig.Groups[gid], kv.currentConfig.Num, shardIDs)
	}
	kv.mu.RUnlock()
	wg.Wait()
}

// TODO: 逻辑不太对，需要 modify 一下
func (kv *ShardKV) getShardIDsByStatus(status ShardStatus) map[int][]int {
	g2s := make(map[int][]int)
	for shardID, shard := range kv.stateMachine {
		if shard.Status == status {
			gid := kv.lastConfig.Shards[shardID]
			if _, ok := g2s[gid]; ok {
				g2s[gid] = append(g2s[gid], shardID)
			} else {
				shardIDs := make([]int, 0)
				g2s[gid] = append(shardIDs, shardID)
			}
		}
	}
	return g2s
}

func (kv *ShardKV) GetShardsData(args *ShardOperationArgs, reply *ShardOperationReply) {
	if _, isLeader := kv.rf.GetState(); !isLeader {
		reply.Err = ErrWrongLeader
		return
	}
	kv.mu.RLock()
	defer kv.mu.RUnlock()
	
	if kv.currentConfig.Num < args.ConfigNum {
		reply.Err = ErrNotReady
		return 
	}

	reply.Shards = make(map[int]map[string]string)
	for _, shardID := range args.ShardIDs {
		reply.Shards[shardID] = kv.stateMachine[shardID].deepCopy()
	}
	reply.LastOperation = make(map[int64]OpContext)
	for clientID, operation := range kv.lastOperation {
		reply.LastOperation[clientID] = operation.deepCopy()
	}

	reply.ConfigNum, reply.Err = args.ConfigNum, OK
}

func (kv *ShardKV) applyInsertShards(shardsInfo *ShardOperationReply) *CommandReply{
	if shardsInfo.ConfigNum == kv.currentConfig.Num {
		for shardId, shardData := range shardsInfo.Shards {
			shard := kv.stateMachine[shardId]
			if shard.Status == Pulling {
				for key, value := range shardData {
					shard.KV[key] = value
				}
				shard.Status = GCing
			} else {
				break;
			}
		}
		for clientId, opContext := range shardsInfo.LastOperation {
			if lastOperation, ok := kv.lastOperation[clientId]; !ok || lastOperation.PrevCommandId < opContext.PrevCommandId {
				kv.lastOperation[clientId] = opContext
			}
		}
		return &CommandReply{OK, ""}
	}
	return &CommandReply{ErrOutDated, ""}
}

func (kv *ShardKV) gcAction() {
	kv.mu.RLock()
	gid2ShardIDS := kv.getShardIDsByStatus(GCing)
	var wg sync.WaitGroup
	for gid, shardIDs := range gid2ShardIDS {
		wg.Add(1)
		go func(servers[]string, configNum int, shardIDs []int) {
			defer wg.Done()
			gcTaskRequest := ShardOperationArgs{configNum, shardIDs}
			for _, server := range servers {
				var gcTaskReply ShardOperationReply
				srv := kv.make_end(server)
				if srv.Call("ShardKV.DeleteShardsData", &gcTaskRequest, &gcTaskReply) && gcTaskReply.Err == OK {
					kv.Execute(NewDeleteShardsCommand(&gcTaskRequest), &CommandReply{})
				} 
			}
		}(kv.lastConfig.Groups[gid], kv.currentConfig.Num, shardIDs)
	}
	kv.mu.RUnlock()
	wg.Wait()
}

func (kv *ShardKV) DeleteShardsData(args *ShardOperationArgs, reply *ShardOperationReply) {
	if _, isLeader := kv.rf.GetState(); !isLeader {
		reply.Err = ErrWrongLeader
		return
	}

	kv.mu.RLock()
	if kv.currentConfig.Num > args.ConfigNum {
		reply.Err = OK
		kv.mu.RUnlock()
		return
	}
	kv.mu.RUnlock()

	var commandReply CommandReply
	kv.Execute(NewDeleteShardsCommand(args), &commandReply)
	reply.Err = commandReply.Err
}

func (kv *ShardKV) applyDeleteShards(shardsInfo *ShardOperationArgs) *CommandReply{
	if shardsInfo.ConfigNum == kv.currentConfig.Num {
		for _, shardId := range shardsInfo.ShardIDs {
			shard := kv.stateMachine[shardId]
			if shard.Status == GCing {
				shard.Status = Serving
			} else if shard.Status == BePulling {
				kv.stateMachine[shardId] = NewShard()
			} else {
				break
			}
		}
		return &CommandReply{OK, ""}
	}
	return &CommandReply{OK, ""}
}

func (kv *ShardKV) checkEntryInCurrentTermAction() {
	if !kv.rf.HasLogInCurrentTerm() {
		kv.Execute(NewEmptyEntryCommand(), &CommandReply{})
	}
}

func (kv *ShardKV) applyEmptyEntry() *CommandReply{
	return &CommandReply{OK, ""}
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
	labgob.Register(Command{})
	labgob.Register(CommandArgs{})
	labgob.Register(shardctrler.Config{})
	labgob.Register(ShardOperationArgs{})
	labgob.Register(ShardOperationReply{})

	kv := new(ShardKV)
	kv.me = me
	kv.dead = 0
	kv.maxRaftState = maxraftstate
	kv.make_end = make_end
	kv.gid = gid
	kv.sc = shardctrler.MakeClerk(ctrlers)
	kv.applyCh = make(chan raft.ApplyMsg)
	kv.rf = raft.Make(servers, me, persister, kv.applyCh)
	kv.lastApplied = 0
	kv.stateMachine = make(map[int]*Shard)
	kv.notifyChans = make(map[int]chan *CommandReply)
	kv.lastOperation = make(map[int64]OpContext)
	kv.lastConfig = shardctrler.DefaultConfig()
	kv.currentConfig = shardctrler.DefaultConfig()

	kv.restoreSnapshot(persister.ReadSnapshot())

	go kv.applier()
	go kv.Monitor(kv.configureAction, ConfigureMonitorTimeout)
	go kv.Monitor(kv.migrationAction, MigrationMonitorTimeout)
	go kv.Monitor(kv.gcAction, GCMonitorTimeout)
	go kv.Monitor(kv.checkEntryInCurrentTermAction, EmptyEntryMonitorTimeout)
	return kv
}

func (kv *ShardKV) Monitor(action func(), timeout time.Duration) {
	for kv.killed() == false {
		if _, isLeader := kv.rf.GetState(); isLeader {
			action()
		}
		time.Sleep(timeout)
	}
}
