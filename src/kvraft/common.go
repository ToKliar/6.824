package kvraft

const (
	OK             = "OK"
	ErrNoKey       = "ErrNoKey"
	ErrWrongLeader = "ErrWrongLeader"
	ErrTimeout	   = "ErrTimeout"
)

const (
	OpGet 		= "Get"
	OpPut		= "Put"
	OpAppend	= "Append"
)

type Err string

type GetReply struct {
	Err   Err
	Value string
}

type CommandArgs struct {
	Key 		string
	Value 		string
	Op 			string
	CommandId 	int
	ClientId	int64
}

type CommandReply struct {
	Err		Err
	Value	string	
}
