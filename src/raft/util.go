package raft

import "log"
import "time"
import "os"
import "strconv"
import "fmt"
import "math/rand"

// Debugging
const Debug = 1
// const heartBeatDuration = 125 * time.Millisecond
const heartBeatDuration = 50 * time.Millisecond

var debugStart time.Time
var debugVerbosity int

type logTopic string
const (
	dClient  logTopic = "CLNT"	// when node started
	dCommit  logTopic = "CMIT"	// when leader commit log
	dDrop    logTopic = "DROP"
	dError   logTopic = "ERRO"
	dInfo    logTopic = "INFO"
	dLeader  logTopic = "LEAD"	// when node become leader
	dLog     logTopic = "LOG1"	// when leader send log to follower
	dLog2    logTopic = "LOG2"	// when follower save log
	dPersist logTopic = "PERS"	// when node persist state
	dSnap    logTopic = "SNAP"	// when node snapshot
	dTerm    logTopic = "TERM"	// when node state change (term change)
	dTest    logTopic = "TEST"
	dTimer   logTopic = "TIMR"	// when reset heartbeat timer or election timer
	dTrace   logTopic = "TRCE"
	dVote    logTopic = "VOTE"	// when node get vote or vote for other node
	dWarn    logTopic = "WARN"
)

func init() {
	debugVerbosity = getVerbosity()
	debugStart = time.Now()

	log.SetFlags(log.Flags() &^ (log.Ldate | log.Ltime))
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

func DPrintf(topic logTopic, format string, a ...interface{}) (n int, err error) {
	if debugVerbosity == 1 {
		t := time.Since(debugStart).Microseconds()
		t /= 100
		prefix := fmt.Sprintf("%06d %v ", t, string(topic))
		format = prefix + format
		log.Printf(format, a...)
	}
	return
}

func RandomElectionTimeout() time.Duration {
	return time.Millisecond * time.Duration(150 + rand.Intn(200))
}

func HeartBeatTimeout() time.Duration {
	return heartBeatDuration
}

func Max(a int, b int) int {
	if a > b {
		return a
	}
	return b
}

func Min(a int, b int) int {
	if a < b {
		return a
	}
	return b
}


