package raft

//
// this is an outline of the API that raft must expose to
// the service (or tester). see comments below for
// each of these functions for more details.
//
// rf = Make(...)
//   create a new Raft server.
// rf.Start(command interface{}) (index, term, isleader)
//   start agreement on a new log entry
// rf.GetState() (term, isLeader)
//   ask a Raft for its current term, and whether it thinks it is leader
// ApplyMsg
//   each time a new entry is committed to the log, each Raft peer
//   should send an ApplyMsg to the service (or tester)
//   in the same server.
//

import (
	//	"bytes"
	"sync"
	"sync/atomic"

	"bytes"
	"sort"
	"time"

	"6.824/labgob"
	"6.824/labrpc"
)

//
// as each Raft peer becomes aware that successive log entries are
// committed, the peer should send an ApplyMsg to the service (or
// tester) on the same server, via the applyCh passed to Make(). set
// CommandValid to true to indicate that the ApplyMsg contains a newly
// committed log entry.
//
// in part 2D you'll want to send other kinds of messages (e.g.,
// snapshots) on the applyCh, but set CommandValid to false for these
// other uses.
//
type ApplyMsg struct {
	CommandValid bool
	Command      interface{}
	CommandIndex int
	CommandTerm  int

	// For 2D:
	SnapshotValid bool
	Snapshot      []byte
	SnapshotTerm  int
	SnapshotIndex int
}

type Entry struct {
	Index   int
	Term    int
	Command interface{}
}

//
// A Go object implementing a single Raft peer.
//
type Raft struct {
	mu        sync.RWMutex        // Lock to protect shared access to this peer's state
	peers     []*labrpc.ClientEnd // RPC end points of all peers
	persister *Persister          // Object to hold this peer's persisted state
	me        int                 // this peer's index into peers[]
	dead      int32               // set by Kill()

	state       int // server state
	currentTerm int // latest term server has seen
	votedFor    int // candidateId that received vote in current term
	commitIndex int // index of highest log entry known to be committed
	lastApplied int // index of highest log entry applied to state machine

	electionTimer  *time.Timer // election timeout
	heartbeatTimer *time.Timer // heartbeat timeout
	log            []Entry

	// for leader server
	nextIndex     []int // index of the next log entry to sender for each server
	matchIndex    []int // index of highest log entry known to be replicated on for each server
	applyCh       chan ApplyMsg
	applyCond     *sync.Cond
	replicateCond []*sync.Cond
}

func (rf *Raft) getFirstLog() Entry {
	if len(rf.log) == 0 {
		rf.log = append(rf.log, Entry{Index: 0, Term: 0})
	}
	return rf.log[0]
}

func (rf *Raft) getLastLog() Entry {
	if len(rf.log) == 0 {
		rf.log = append(rf.log, Entry{Index: 0, Term: 0})
	}
	return rf.log[len(rf.log)-1]
}

func (rf *Raft) GetRaftStateSize() int {
	return rf.persister.RaftStateSize()
}

func (rf *Raft) appendNewEntry(command interface{}) Entry {
	lastIndex := rf.getLastLog().Index
	entry := Entry{
		Index:   lastIndex + 1,
		Term:    rf.currentTerm,
		Command: command,
	}
	DPrintf(dLeader, "S%d Append New Entry %v", rf.me, entry)
	rf.log = append(rf.log, entry)
	rf.persist()
	return entry
}

func (rf *Raft) HasLogInCurrentTerm() bool {
	rf.mu.RLock()
	defer rf.mu.RUnlock()
	for index := len(rf.log) - 1; index >= 0; index-- {
		if rf.log[index].Term <= rf.currentTerm {
			return rf.log[index].Term == rf.currentTerm
		}
	}
	return false
}

// 对新的定义
// 如果两份日志最后条目的任期不同，任期号大的日志新
// 如果两份日志最后条目的任期相同，日志比较长的那个更新
func (rf *Raft) isLogNewer(lastLogTerm int, lastLogIndex int) bool {
	if rf.getLastLog().Term != lastLogTerm {
		return rf.getLastLog().Term < lastLogTerm
	}
	return rf.getLastLog().Index <= lastLogIndex
}

func (rf *Raft) matchLog(prevLogTerm int, prevLogIndex int) bool {
	lastIndex := rf.getLastLog().Index
	if lastIndex < prevLogIndex {
		DPrintf(dInfo, "S%d Match Log Fail PLT:%d PLI:%d", rf.me, prevLogTerm, prevLogIndex)
		return false
	}
	firstIndex := rf.getFirstLog().Index
	entry := rf.log[prevLogIndex-firstIndex]
	if entry.Term != prevLogTerm {
		DPrintf(dInfo, "S%d Match Log Fail PLT:%d PLI:%d", rf.me, prevLogTerm, prevLogIndex)
		return false
	}
	DPrintf(dInfo, "S%d Match Log Success PLT:%d PLI:%d", rf.me, prevLogTerm, prevLogIndex)
	return true
}

// return currentTerm and whether this server
// believes it is the leader.
func (rf *Raft) GetState() (int, bool) {
	var term int
	var isleader bool
	rf.mu.Lock()
	term = rf.currentTerm
	isleader = rf.state == StateLeader
	rf.mu.Unlock()
	return term, isleader
}

//
// save Raft's persistent state to stable storage,
// where it can later be retrieved after a crash and restart.
// see paper's Figure 2 for a description of what should be persistent.
//
func (rf *Raft) persist() {
	w := new(bytes.Buffer)
	e := labgob.NewEncoder(w)
	e.Encode(rf.currentTerm)
	e.Encode(rf.votedFor)
	e.Encode(rf.log)
	data := w.Bytes()
	rf.persister.SaveRaftState(data)
	DPrintf(dPersist, "S%d Save State T:%d VF:%d First-Log:%v Last-Log:%v", rf.me, rf.currentTerm, rf.votedFor, rf.log[0], rf.log[len(rf.log)-1])
}

//
// restore previously persisted state.
//
func (rf *Raft) readPersist(data []byte) {
	if data == nil || len(data) < 1 { // bootstrap without any state?
		return
	}
	r := bytes.NewBuffer(data)
	d := labgob.NewDecoder(r)
	var currentTerm int
	var votedFor int
	log := make([]Entry, 0)
	if d.Decode(&currentTerm) != nil || d.Decode(&votedFor) != nil || d.Decode(&log) != nil {
		DPrintf(dError, "S%d Can't Decode Persist", rf.me)
	} else {
		rf.currentTerm = currentTerm
		rf.votedFor = votedFor
		rf.log = make([]Entry, len(log), len(log))
		copy(rf.log, log)
		rf.commitIndex = rf.log[0].Index
		rf.lastApplied = rf.log[0].Index
		DPrintf(dPersist, "S%d Load State T:%d VF:%d First-Log:%v Last-Log:%v", rf.me, rf.currentTerm, rf.votedFor, rf.log[0], rf.log[len(rf.log)-1])
	}
}

func (rf *Raft) ticker() {
	for rf.killed() == false {
		select {
		case <-rf.electionTimer.C:
			rf.mu.Lock()
			DPrintf(dTimer, "S%d Not Leader, checking election timeout", rf.me)
			rf.StartElection()
			rf.electionTimer.Reset(RandomElectionTimeout())
			rf.mu.Unlock()
		case <-rf.heartbeatTimer.C:
			rf.mu.Lock()
			if rf.state == StateLeader {
				DPrintf(dTimer, "S%d Broadcast heartbeat timer timeout, reseting HBT", rf.me)
				rf.DoHeartBeat(true)
				rf.heartbeatTimer.Reset(HeartBeatTimeout())
			}
			rf.mu.Unlock()
		}
	}
}

func (rf *Raft) applier() {
	for rf.killed() == false {
		rf.mu.Lock()
		for rf.lastApplied >= rf.commitIndex {
			rf.applyCond.Wait()
		}
		firstIndex, commitIndex, lastApplied := rf.getFirstLog().Index, rf.commitIndex, rf.lastApplied
		DPrintf(dCommit, "S%d Begin to Apply, LA:%d, CI:%d, FI:%d", rf.me, lastApplied, commitIndex, firstIndex)
		entries := make([]Entry, commitIndex-lastApplied)
		copy(entries, rf.log[lastApplied+1-firstIndex:commitIndex+1-firstIndex])
		rf.mu.Unlock()
		for _, entry := range entries {
			rf.applyCh <- ApplyMsg{
				CommandValid: true,
				Command:      entry.Command,
				CommandIndex: entry.Index,
				CommandTerm:  entry.Term,
			}
		}
		rf.mu.Lock()
		rf.lastApplied = Max(rf.lastApplied, commitIndex)
		if len(entries) > 0 {
			DPrintf(dCommit, "S%d Nothing left to apply, await (LA:%d = CI:%d), first-entry %v, last-entry %v", rf.me, rf.lastApplied, rf.commitIndex, entries[0], entries[len(entries)-1])
		} else {
			DPrintf(dCommit, "S%d Nothing left to apply, await (LA:%d = CI:%d), no entry", rf.me, rf.lastApplied, rf.commitIndex)
		}

		rf.mu.Unlock()
	}
}

func (rf *Raft) replicator(peer int) {
	rf.replicateCond[peer].L.Lock()
	defer rf.replicateCond[peer].L.Unlock()
	for rf.killed() == false {
		for !rf.canReplicate(peer) {
			rf.replicateCond[peer].Wait()
		}
		rf.replicateEntries(peer)
	}
}

func (rf *Raft) canReplicate(peer int) bool {
	rf.mu.RLock()
	defer rf.mu.RUnlock()
	return rf.state == StateLeader && rf.matchIndex[peer] < rf.getLastLog().Index
}

//
// example RequestVote RPC arguments structure.
// field names must start with capital letters!
//
type RequestVoteArgs struct {
	Term         int // candidate's term
	CandidateId  int // candidate requesting vote
	LastLogIndex int // index of candidate's last log entry
	LastLogTerm  int // term of candidate's last log entry
}

//
// example RequestVote RPC reply structure.
// field names must start with capital letters!
//
type RequestVoteReply struct {
	Term        int  // currentTerm
	VoteGranted bool // true means candidate received vote
}

func (rf *Raft) StartElection() {
	rf.state = StateCandidate
	rf.currentTerm += 1
	requestVoteArgs := RequestVoteArgs{
		Term:         rf.currentTerm,
		CandidateId:  rf.me,
		LastLogTerm:  rf.getLastLog().Term,
		LastLogIndex: rf.getLastLog().Index,
	}
	getVotedCount := 1
	rf.votedFor = rf.me
	rf.persist()
	DPrintf(dVote, "S%d start election with RequestVoteArgs %v", rf.me, requestVoteArgs)
	for idx := range rf.peers {
		if idx == rf.me {
			continue
		}
		go func(i int) {
			requestVoteReply := RequestVoteReply{}

			ok := rf.sendRequestVote(i, &requestVoteArgs, &requestVoteReply)
			if ok {
				rf.mu.Lock()
				defer rf.mu.Unlock()
				defer rf.persist()
				if rf.currentTerm == requestVoteReply.Term && rf.state == StateCandidate && rf.currentTerm == requestVoteArgs.Term {
					if requestVoteReply.VoteGranted {
						getVotedCount += 1
						DPrintf(dVote, "S%d Get Vote from S%d", rf.me, i)
						if getVotedCount > len(rf.peers)/2 {
							DPrintf(dLeader, "S%d Achieved Majority for T:%d, converting to Leader", rf.me, rf.currentTerm)
							rf.state = StateLeader
							lastLogIndex := rf.getLastLog().Index
							for i := 0; i < len(rf.peers); i++ {
								rf.matchIndex[i], rf.nextIndex[i] = 0, lastLogIndex+1
							}
							rf.DoHeartBeat(true)
						}
					} else {
						DPrintf(dVote, "S%d Don't Get Vote from S%d", rf.me, i)
						if requestVoteReply.Term > rf.currentTerm {
							DPrintf(dTerm, "S%d Term is higher, updating {%d > %d}, handling request vote, converting to Follower", rf.me, requestVoteReply.Term, rf.currentTerm)
							rf.state = StateFollower
							rf.currentTerm = requestVoteReply.Term
							rf.votedFor = -1
						}
					}
				}
			}
		}(idx)
	}
}

//
// example RequestVote RPC handler.
//
func (rf *Raft) RequestVote(args *RequestVoteArgs, reply *RequestVoteReply) {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	defer rf.persist()

	if args.Term < rf.currentTerm || (args.Term == rf.currentTerm && rf.votedFor != -1 && rf.votedFor != args.CandidateId) {
		DPrintf(dVote, "S%d Doesn't Grant Vote for S%d in T:%d", rf.me, args.CandidateId, args.Term)
		reply.Term = rf.currentTerm
		reply.VoteGranted = false
		return
	}
	if args.Term > rf.currentTerm {
		DPrintf(dTerm, "S%d Term is higher, updating {%d > %d}, request vote", rf.me, args.Term, rf.currentTerm)
		rf.state = StateFollower
		rf.currentTerm, rf.votedFor = args.Term, -1
	}
	if !rf.isLogNewer(args.LastLogTerm, args.LastLogIndex) {
		reply.Term = rf.currentTerm
		reply.VoteGranted = false
		return
	}

	rf.votedFor = args.CandidateId
	rf.electionTimer.Reset(RandomElectionTimeout())
	reply.Term = rf.currentTerm
	reply.VoteGranted = true
	DPrintf(dVote, "S%d Grant Vote for S%d in T:%d", rf.me, args.CandidateId, reply.Term)
}

type AppendEntriesArgs struct {
	Term         int
	LeaderId     int
	PrevLogIndex int
	PrevLogTerm  int
	LeaderCommit int
	Entries      []Entry
}

type AppendEntriesReply struct {
	Term          int
	Success       bool
	ConflictTerm  int
	ConflictIndex int
}

func (rf *Raft) DoHeartBeat(isHeartBeat bool) {
	for peer := range rf.peers {
		if peer == rf.me {
			continue
		}
		if isHeartBeat {
			go rf.replicateEntries(peer)
		} else {
			rf.replicateCond[peer].Signal()
		}
	}
	rf.heartbeatTimer.Reset(HeartBeatTimeout())
}

func (rf *Raft) replicateEntries(peer int) {
	rf.mu.RLock()
	if rf.state != StateLeader {
		rf.mu.RUnlock()
		return
	}
	prevLogIndex := rf.nextIndex[peer] - 1
	if prevLogIndex < rf.getFirstLog().Index {
		args := InstallSnapshotArgs{
			Term:              rf.currentTerm,
			LeaderId:          rf.me,
			LastIncludedIndex: rf.getFirstLog().Index,
			LastIncludedTerm:  rf.getFirstLog().Term,
			Data:              rf.persister.ReadSnapshot(),
		}
		DPrintf(dLog, "S%d -> S%d Sending Install Snapshot Old LII:%d PLI:%d LII:%d LIT:%d", rf.me, peer, rf.getFirstLog().Index,
			prevLogIndex, args.LastIncludedIndex, args.LastIncludedTerm)
		reply := InstallSnapshotReply{}
		rf.mu.RUnlock()
		if rf.sendInstallSnapshot(peer, &args, &reply) {
			rf.mu.Lock()
			if rf.state == StateLeader {
				rf.handleInstallSnapshot(peer, args, reply)
			}
			rf.mu.Unlock()
		}
	} else {
		firstIndex := rf.getFirstLog().Index
		DPrintf(dLog, "S%d -> S%d Replicate prevLogIndex: %d, firstIndex:%d\n", rf.me, peer, prevLogIndex, firstIndex)
		prevLog := rf.log[prevLogIndex-firstIndex]
		args := AppendEntriesArgs{
			Term:         rf.currentTerm,
			LeaderId:     rf.me,
			PrevLogIndex: prevLogIndex,
			PrevLogTerm:  prevLog.Term,
			LeaderCommit: rf.commitIndex,
			Entries:      rf.log[prevLogIndex-firstIndex+1:],
		}
		if len(args.Entries) > 0 {
			DPrintf(dLog, "S%d -> S%d Sending PLI:%d PLT:%d T:%d LC:%d First-Entry:%v Last-Entry:%v", rf.me, peer,
				args.PrevLogIndex, args.PrevLogTerm, args.Term, args.LeaderCommit, args.Entries[0], args.Entries[len(args.Entries)-1])
		} else {
			DPrintf(dLog, "S%d -> S%d Sending PLI:%d PLT:%d T:%d LC:%d No-Entry", rf.me, peer,
				args.PrevLogIndex, args.PrevLogTerm, args.Term, args.LeaderCommit)
		}
		reply := AppendEntriesReply{}
		rf.mu.RUnlock()
		if rf.sendAppendEntries(peer, &args, &reply) {
			rf.mu.Lock()
			if rf.state == StateLeader {
				rf.handleAppendEntries(peer, args, reply)
			}
			rf.mu.Unlock()
		}
	}
}

func (rf *Raft) handleAppendEntries(peer int, args AppendEntriesArgs, reply AppendEntriesReply) {
	// 判断的时候需要确认rf的状态是leader
	// 只有leader可以进行后续的处理
	if rf.state != StateLeader || rf.currentTerm != args.Term {
		DPrintf(dError, "S%d is not leader or changes from previous term: %d", rf.me, args.Term)
		return
	}

	if reply.Term > rf.currentTerm {
		DPrintf(dLeader, "S%d Append Entries Fail", rf.me)
		DPrintf(dTerm, "S%d Term is higher, updating {%d > %d}, handling append entries, converting to Follower", rf.me, reply.Term, rf.currentTerm)
		rf.state = StateFollower
		rf.currentTerm, rf.votedFor = reply.Term, -1
		rf.electionTimer.Reset(RandomElectionTimeout())
		rf.persist()
		return
	}

	if reply.Success {
		DPrintf(dLeader, "S%d -> S%d Append Entries Success", rf.me, peer)
		nextIndex := args.PrevLogIndex + len(args.Entries) + 1
		rf.nextIndex[peer] = Max(nextIndex, rf.nextIndex[peer])
		rf.matchIndex[peer] = rf.nextIndex[peer] - 1
		if rf.getLastLog().Index > rf.commitIndex {
			indexes := make([]int, len(rf.peers), len(rf.peers))
			copy(indexes, rf.matchIndex)
			indexes[rf.me] = rf.getLastLog().Index
			sort.Ints(indexes)
			commitIndex := indexes[len(rf.peers)/2]

			firstLogIndex := rf.getFirstLog().Index
			if commitIndex > rf.commitIndex && rf.log[commitIndex-firstLogIndex].Term == rf.currentTerm {
				rf.commitIndex = commitIndex
				DPrintf(dLeader, "S%d Update CI:%d", rf.me, rf.commitIndex)
				rf.applyCond.Signal()
			}
		}
		return
	}

	DPrintf(dLeader, "S%d Append Entries Fail", rf.me)
	if reply.ConflictTerm == -1 {
		rf.nextIndex[peer] = reply.ConflictIndex
	} else {
		firstIndex := rf.getFirstLog().Index
		index := rf.getLastLog().Index
		find := false
		for ; index >= firstIndex; index-- {
			if rf.log[index-firstIndex].Term == reply.ConflictTerm {
				find = true
				break
			}
		}
		if find {
			rf.nextIndex[peer] = index + 1
		} else {
			rf.nextIndex[peer] = reply.ConflictIndex
		}
	}
	go rf.replicateEntries(peer)
}

func (rf *Raft) AppendEntries(args *AppendEntriesArgs, reply *AppendEntriesReply) {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	defer rf.persist()

	if args.Term < rf.currentTerm {
		reply.Term = rf.currentTerm
		reply.Success = false
		DPrintf(dError, "S%d Term T:%d Bigger Than Leader S%d T:%d", rf.me, rf.currentTerm, args.LeaderId, args.Term)
		DPrintf(dLog, "S%d -> S%d Replying T:%d S:%t CT:%d CI:%d", rf.me, args.LeaderId, reply.Term, reply.Success, reply.ConflictTerm, reply.ConflictIndex)
		return
	}

	if args.Term > rf.currentTerm {
		DPrintf(dTerm, "S%d Term is higher, updating {%d > %d}, append entries", rf.me, args.Term, rf.currentTerm)
		rf.currentTerm = args.Term
		rf.votedFor = -1
	}
	rf.state = StateFollower
	rf.electionTimer.Reset(RandomElectionTimeout())

	if args.PrevLogIndex < rf.getFirstLog().Index {
		reply.Term = 0
		reply.Success = false
		DPrintf(dError, "S%d doesn's contain an entry at PLI:%d, first entry I:%d", rf.me, args.PrevLogIndex, rf.getFirstLog().Index)
		DPrintf(dLog, "S%d -> S%d Replying T:%d S:%t CT:%d CI:%d", rf.me, args.LeaderId, reply.Term, reply.Success, reply.ConflictTerm, reply.ConflictIndex)
		return
	}

	if !rf.matchLog(args.PrevLogTerm, args.PrevLogIndex) {
		reply.Term = rf.currentTerm
		reply.Success = false
		lastIndex := rf.getLastLog().Index
		DPrintf(dLog, "S%d FI:%d LI:%d PI:%d", rf.me, rf.getFirstLog().Index, lastIndex, args.PrevLogIndex)
		if lastIndex < args.PrevLogIndex {
			reply.ConflictTerm = -1
			reply.ConflictIndex = lastIndex + 1
		} else {
			firstIndex := rf.getFirstLog().Index
			reply.ConflictTerm = rf.log[args.PrevLogIndex-firstIndex].Term
			index := args.PrevLogIndex - 1
			for index >= firstIndex && rf.log[index-firstIndex].Term == reply.ConflictTerm {
				index--
			}
			reply.ConflictIndex = index
		}
		DPrintf(dLog, "S%d -> S%d Replying T:%d S:%t CT:%d CI:%d", rf.me, args.LeaderId, reply.Term, reply.Success, reply.ConflictTerm, reply.ConflictIndex)
		return
	}

	firstIndex := rf.getFirstLog().Index
	for index, entry := range args.Entries {
		if entry.Index-firstIndex >= len(rf.log) || rf.log[entry.Index-firstIndex].Term != entry.Term {
			rf.log = shrinkEntriesArray(append(rf.log[:entry.Index-firstIndex], args.Entries[index:]...))
			DPrintf(dLog2, "S%d Save First-Log %v, Last-Log %v, All-First-Log %v", rf.me, args.Entries[index], args.Entries[len(args.Entries)-1], rf.log[0])
			break
		}
	}

	if args.LeaderCommit > rf.commitIndex {
		oldCommitIndex := rf.commitIndex
		rf.commitIndex = Min(args.LeaderCommit, rf.getLastLog().Index)
		if rf.commitIndex != oldCommitIndex {
			DPrintf(dInfo, "S%d Update CI:%d", rf.me, rf.commitIndex)
			if rf.commitIndex > rf.lastApplied {
				rf.applyCond.Signal()
			}
		}
	}

	reply.Term = rf.currentTerm
	reply.Success = true
	DPrintf(dLog, "S%d -> S%d Replying T:%d S:%t CT:%d CI:%d", rf.me, args.LeaderId, reply.Term, reply.Success, reply.ConflictTerm, reply.ConflictIndex)
}

type InstallSnapshotArgs struct {
	Term              int
	LeaderId          int
	LastIncludedIndex int
	LastIncludedTerm  int
	Data              []byte
}

type InstallSnapshotReply struct {
	Term int
}

func (rf *Raft) handleInstallSnapshot(peer int, args InstallSnapshotArgs, reply InstallSnapshotReply) {
	// 判断的时候需要确认rf的状态是leader
	// 只有leader可以进行后续的处理
	if rf.state != StateLeader || rf.currentTerm != args.Term {
		DPrintf(dError, "S%d is not leader or changes from previous term: %d", rf.me, args.Term)
		return
	}

	if reply.Term > rf.currentTerm {
		DPrintf(dLeader, "S%d Install Snapshot Fail", rf.me)
		DPrintf(dTerm, "S%d Term is higher, updating {%d > %d}, handling install snapshot, converting to Follower", rf.me, reply.Term, rf.currentTerm)
		rf.state = StateFollower
		rf.currentTerm, rf.votedFor = reply.Term, -1
		rf.electionTimer.Reset(RandomElectionTimeout())
		rf.persist()
		return
	}
	rf.nextIndex[peer] = Max(args.LastIncludedIndex+1, rf.nextIndex[peer])
	rf.matchIndex[peer] = Max(args.LastIncludedIndex, rf.matchIndex[peer])
	if rf.getLastLog().Index > rf.commitIndex {
		indexes := make([]int, len(rf.peers), len(rf.peers))
		copy(indexes, rf.matchIndex)
		indexes[rf.me] = rf.getLastLog().Index
		sort.Ints(indexes)
		commitIndex := indexes[len(rf.peers)/2]

		firstLogIndex := rf.getFirstLog().Index
		if commitIndex > rf.commitIndex && rf.log[commitIndex-firstLogIndex].Term == rf.currentTerm {
			rf.commitIndex = commitIndex
			DPrintf(dLeader, "S%d Update CI:%d", rf.me, rf.commitIndex)
			rf.applyCond.Signal()
		}
	}
}

func (rf *Raft) InstallSnapshot(args *InstallSnapshotArgs, reply *InstallSnapshotReply) {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	DPrintf(dPersist, "S%d <- S%d Receive Install Snapshot T:%d LII:%d, LIT:%d, Data:[%v]", rf.me, args.LeaderId, args.Term,
		args.LastIncludedIndex, args.LastIncludedTerm, args.Data)

	reply.Term = rf.currentTerm
	if args.Term < rf.currentTerm {
		return
	}

	if args.Term > rf.currentTerm {
		rf.currentTerm = args.Term
		rf.votedFor = -1
		rf.persist()
	}

	rf.state = StateFollower
	rf.electionTimer.Reset(RandomElectionTimeout())

	if args.LastIncludedIndex <= rf.commitIndex {
		return
	}

	go func() {
		rf.applyCh <- ApplyMsg{
			SnapshotValid: true,
			Snapshot:      args.Data,
			SnapshotTerm:  args.LastIncludedTerm,
			SnapshotIndex: args.LastIncludedIndex,
		}
	}()

}

//
// A service wants to switch to snapshot.  Only do so if Raft hasn't
// have more recent info since it communicate the snapshot on applyCh.
//
func (rf *Raft) CondInstallSnapshot(lastIncludedTerm int, lastIncludedIndex int, snapshot []byte) bool {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	DPrintf(dPersist, "S%d Cond Install Snapshot LII:%d, LIT:%d", rf.me, lastIncludedIndex, lastIncludedIndex)

	if lastIncludedIndex <= rf.commitIndex {
		DPrintf(dPersist, "S%d Reject Snapshot LII:%d, Commit Index is Bigger CI:%d", rf.me, lastIncludedIndex, rf.commitIndex)
		return false
	}

	if lastIncludedIndex > rf.getLastLog().Index {
		rf.log = make([]Entry, 1)
	} else {
		rf.log = shrinkEntriesArray(rf.log[lastIncludedIndex-rf.getFirstLog().Index:])
	}

	rf.log[0].Command = nil
	rf.log[0].Term, rf.log[0].Index = lastIncludedTerm, lastIncludedIndex
	rf.lastApplied, rf.commitIndex = lastIncludedIndex, lastIncludedIndex

	rf.persister.SaveStateAndSnapshot(rf.encodeState(), snapshot)
	DPrintf(dPersist, "S%d After Snapshot State:%d, Term:%d, CI:%d, LA:%d, FirstLog:%v, LastLog:%v, LII:%d, LIT:%d", rf.me, rf.state, rf.currentTerm, rf.commitIndex, rf.lastApplied, rf.getFirstLog(), rf.getLastLog(), lastIncludedIndex, lastIncludedTerm)
	return true
}

func (rf *Raft) ReplaceLogSnapshot(lastIncludedIndex int, snapshot []byte) {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	DPrintf(dPersist, "S%d Install Snapshot from Server LII:%d", rf.me, lastIncludedIndex)
	if lastIncludedIndex < rf.getFirstLog().Index {
		DPrintf(dPersist, "S%d Reject Snapshot LII:%d, First Log is Bigger I:%d", rf.me, lastIncludedIndex, rf.getFirstLog().Index)
		return
	}
	if lastIncludedIndex > rf.commitIndex {
		DPrintf(dPersist, "S%d Reject Snapshot LII:%d, Commit Index is Smaller CI:%d", rf.me, lastIncludedIndex, rf.commitIndex)
		return
	}

	rf.log = shrinkEntriesArray(rf.log[lastIncludedIndex-rf.getFirstLog().Index:])

	rf.persister.SaveStateAndSnapshot(rf.encodeState(), snapshot)
	DPrintf(dPersist, "S%d State-Size:%d", rf.me, rf.persister.RaftStateSize())
	// for i := range rf.peers {
	// 	if i == rf.me {
	// 		continue
	// 	}
	// 	go rf.syncSnapshotWith(i, lastIncludedIndex)
	// }
	DPrintf(dPersist, "S%d After Snapshot State:%d, Term:%d, CI:%d, LA:%d, FirstLog:%v, LastLog:%v, LII:%d", rf.me, rf.state, rf.currentTerm, rf.commitIndex, rf.lastApplied, rf.getFirstLog(), rf.getLastLog(), lastIncludedIndex)
}

// the service says it has created a snapshot that has
// all info up to and including index. this means the
// service no longer needs the log through (and including)
// that index. Raft should now trim its log as much as possible.
func (rf *Raft) Snapshot(index int, snapshot []byte) {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	snapshotIndex := rf.getFirstLog().Index
	if index <= snapshotIndex {
		DPrintf(dPersist, "S%d Reject Replace Log with I:%d FI:%d", rf.me, index, snapshotIndex)
		return
	}
	rf.log = shrinkEntriesArray(rf.log[index-snapshotIndex:])
	rf.log[0].Command = nil
	rf.persister.SaveStateAndSnapshot(rf.encodeState(), snapshot)
	DPrintf(dPersist, "S%d After Snapshot State:%d, Term:%d, CI:%d, LA:%d, FirstLog:%v, LastLog:%v, SI:%d, Old SI:%d", rf.me, rf.state, rf.currentTerm, rf.commitIndex, rf.lastApplied, rf.getFirstLog(), rf.getLastLog(), index, snapshotIndex)
}

func (rf *Raft) encodeState() []byte {
	w := new(bytes.Buffer)
	e := labgob.NewEncoder(w)
	e.Encode(rf.currentTerm)
	e.Encode(rf.votedFor)
	e.Encode(rf.log)
	return w.Bytes()
}

func shrinkEntriesArray(log []Entry) []Entry {
	return append([]Entry{}, log...)
}

//
// example code to send a RequestVote RPC to a server.
// server is the index of the target server in rf.peers[].
// expects RPC arguments in args.
// fills in *reply with RPC reply, so caller should
// pass &reply.
// the types of the args and reply passed to Call() must be
// the same as the types of the arguments declared in the
// handler function (including whether they are pointers).
//
// The labrpc package simulates a lossy network, in which servers
// may be unreachable, and in which requests and replies may be lost.
// Call() sends a request and waits for a reply. If a reply arrives
// within a timeout interval, Call() returns true; otherwise
// Call() returns false. Thus Call() may not return for a while.
// A false return can be caused by a dead server, a live server that
// can't be reached, a lost request, or a lost reply.
//
// Call() is guaranteed to return (perhaps after a delay) *except* if the
// handler function on the server side does not return.  Thus there
// is no need to implement your own timeouts around Call().
//
// look at the comments in ../labrpc/labrpc.go for more details.
//
// if you're having trouble getting RPC to work, check that you've
// capitalized all field names in structs passed over RPC, and
// that the caller passes the address of the reply struct with &, not
// the struct itself.
//
func (rf *Raft) sendRequestVote(server int, args *RequestVoteArgs, reply *RequestVoteReply) bool {
	ok := rf.peers[server].Call("Raft.RequestVote", args, reply)
	return ok
}

func (rf *Raft) sendAppendEntries(server int, args *AppendEntriesArgs, reply *AppendEntriesReply) bool {
	ok := rf.peers[server].Call("Raft.AppendEntries", args, reply)
	return ok
}

func (rf *Raft) sendInstallSnapshot(server int, args *InstallSnapshotArgs, reply *InstallSnapshotReply) bool {
	ok := rf.peers[server].Call("Raft.InstallSnapshot", args, reply)
	return ok
}

//
// the service using Raft (e.g. a k/v server) wants to start
// agreement on the next command to be appended to Raft's log. if this
// server isn't the leader, returns false. otherwise start the
// agreement and return immediately. there is no guarantee that this
// command will ever be committed to the Raft log, since the leader
// may fail or lose an election. even if the Raft instance has been killed,
// this function should return gracefully.
//
// the first return value is the index that the command will appear at
// if it's ever committed. the second return value is the current
// term. the third return value is true if this server believes it is
// the leader.
//
func (rf *Raft) Start(command interface{}) (int, int, bool) {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	if rf.state != StateLeader {
		return -1, -1, false
	}
	newLog := rf.appendNewEntry(command)
	rf.DoHeartBeat(false)
	return newLog.Index, newLog.Term, true
}

//
// the tester doesn't halt goroutines created by Raft after each test,
// but it does call the Kill() method. your code can use killed() to
// check whether Kill() has been called. the use of atomic avoids the
// need for a lock.
//
// the issue is that long-running goroutines use memory and may chew
// up CPU time, perhaps causing later tests to fail and generating
// confusing debug output. any goroutine with a long-running loop
// should call killed() to check whether it should stop.
//
func (rf *Raft) Kill() {
	atomic.StoreInt32(&rf.dead, 1)
	// Your code here, if desired.
}

func (rf *Raft) killed() bool {
	z := atomic.LoadInt32(&rf.dead)
	return z == 1
}

//
// the service or tester wants to create a Raft server. the ports
// of all the Raft servers (including this one) are in peers[]. this
// server's port is peers[me]. all the servers' peers[] arrays
// have the same order. persister is a place for this server to
// save its persistent state, and also initially holds the most
// recent saved state, if any. applyCh is a channel on which the
// tester or service expects Raft to send ApplyMsg messages.
// Make() must return quickly, so it should start goroutines
// for any long-running work.
//
func Make(peers []*labrpc.ClientEnd, me int,
	persister *Persister, applyCh chan ApplyMsg) *Raft {
	rf := &Raft{}
	rf.peers = peers
	rf.persister = persister
	rf.me = me
	rf.dead = 0

	rf.currentTerm = 0
	rf.votedFor = -1
	rf.commitIndex = 0
	rf.lastApplied = 0
	rf.state = StateFollower
	rf.applyCh = applyCh
	rf.applyCond = sync.NewCond(&rf.mu)
	rf.replicateCond = make([]*sync.Cond, len(peers))

	rf.nextIndex = make([]int, len(peers))
	rf.matchIndex = make([]int, len(peers))

	rf.electionTimer = time.NewTimer(RandomElectionTimeout())
	rf.heartbeatTimer = time.NewTimer(HeartBeatTimeout())
	rf.log = make([]Entry, 1)

	// initialize from state persisted before a crash
	rf.readPersist(persister.ReadRaftState())
	DPrintf(dClient, "S%d Started at T:%d VF:%d LLI:%d", rf.me, rf.currentTerm, rf.votedFor, rf.getLastLog().Index)

	if rf.getFirstLog().Index > rf.lastApplied {
		rf.lastApplied = rf.getFirstLog().Index
	}

	lastLog := rf.getLastLog()
	for i := 0; i < len(peers); i++ {
		rf.matchIndex[i], rf.nextIndex[i] = 0, lastLog.Index+1
		if i != rf.me {
			rf.replicateCond[i] = sync.NewCond(&sync.Mutex{})
			go rf.replicator(i)
		}
	}
	go rf.ticker()
	go rf.applier()
	return rf
}
