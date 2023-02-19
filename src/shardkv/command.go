package shardkv

import (
	"fmt"

	"6.824/shardctrler"
)

type Command struct {
	Op   CommandType
	Data interface{}
}

type CommandType uint8

const (
	Operation CommandType = iota
	Configuration
	InsertShards
	DeleteShards
	EmptyEntry
)

func (cm *Command) String() string {
	return fmt.Sprintf("{Type:%v Data:%v}", cm.Op, cm.Data)
}

func NewOperationCommand(args *CommandArgs) Command {
	return Command{Operation, *args}
}

func NewConfigurationCommand(config *shardctrler.Config) Command {
	return Command{Configuration, *config}
}

func NewInsertShardsCommand(reply *ShardOperationReply) Command {
	return Command{InsertShards, *reply}
}

func NewDeleteShardsCommand(args *ShardOperationArgs) Command {
	return Command{DeleteShards, *args}
}

func NewEmptyEntryCommand() Command {
	return Command{EmptyEntry, nil}
}
