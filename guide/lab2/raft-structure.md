# Raft Structure Advice

A Raft instance has to deal with the arrival of external events (Start() calls, AppendEntries and RequestVote RPCs, and RPC replies), and it has to execute periodic tasks (elections and heart-beats). There are many ways to structure your Raft code to manage these activities; this document outlines a few ideas.

Raft实例必须处理外部事件的到达(Start()调用、AppendEntries和RequestVote RPC以及RPC应答)，并且它必须执行定期任务(选举和心跳)。有很多方法可以构建Raft代码来管理这些活动；本文档概述了一些想法。

Each Raft instance has a bunch of state (the log, the current index, &c) which must be updated in response to events arising in concurrent goroutines. The Go documentation points out that the goroutines can perform the updates directly using shared data structures and locks, or by passing messages on channels. Experience suggests that for Raft it is most straightforward to use shared data and locks.

每个Raft实例都有一堆状态(日志、当前索引和&c)，必须更新这些状态以响应并发goroutine中发生的事件。Go文档指出，goroutine可以直接使用共享数据结构和锁，或者通过在通道上传递消息来执行更新。经验表明，对于Raft来说，最直接的方法是使用共享数据和锁。

A Raft instance has two time-driven activities: the leader must send heart-beats, and others must start an election if too much time has passed since hearing from the leader. It's probably best to drive each of these activities with a dedicated long-running goroutine, rather than combining multiple activities into a single goroutine.

Raft实例有两个时间驱动的活动：leader必须发送heartbeat，如果从leader收到消息后过了太长时间，其他follower必须启动选举。最好是用一个专门的长时间运行的goroutine来驱动每个活动，而不是将多个活动组合成一个goroutine。

The management of the election timeout is a common source of headaches. Perhaps the simplest plan is to maintain a variable in the Raft struct containing the last time at which the peer heard from the leader, and to have the election timeout goroutine periodically check to see whether the time since then is greater than the timeout period. It's easiest to use time.Sleep() with a small constant argument to drive the periodic checks. Don't use time.Ticker and time.Timer; they are tricky to use correctly.

选举超时的管理是一个常见的令人头疼的问题。也许最简单的计划是在Raft结构中维护一个变量，其中包含节点最后一次收到leader消息的时间，并使用选举超时goroutine程序定期检查从那时起经过的时间是否大于超时时间。使用带有小常量参数的time.Sleep()来驱动定期检查是最简单的。不要使用time.Ticker和time.Timer；正确使用它们很困难。

You'll want to have a separate long-running goroutine that sends committed log entries in order on the applyCh. It must be separate, since sending on the applyCh can block; and it must be a single goroutine, since otherwise it may be hard to ensure that you send log entries in log order. The code that advances commitIndex will need to kick the apply goroutine; it's probably easiest to use a condition variable (Go's sync.Cond) for this.

你将需要一个独立的长时间运行的goroutine，它在applyCh上按顺序发送提交的日志条目。它必须是独立的，因为在applyCh上发送会阻塞；而且它必须是一个单独的goroutine，否则就很难确保按日志顺序发送日志条目。递增commitIndex的代码将需要基于apply goroutine；使用条件变量(Go的sync.Cond)可能是最简单的方法。

Each RPC should probably be sent (and its reply processed) in its own goroutine, for two reasons: so that unreachable peers don't delay the collection of a majority of replies, and so that the heartbeat and election timers can continue to tick at all times. It's easiest to do the RPC reply processing in the same goroutine, rather than sending reply information over a channel.

应该在节点自己的goroutine中发送每个RPC(并处理它的应答)，原因有两个：这样不可达的节点就不会延迟大部分应答的收集，这样心跳和选举计时器就可以在任何时候继续计时和发送。在相同的goroutine中执行RPC应答处理，而不是通过通道发送应答信息，这是最容易的。

Keep in mind that the network can delay RPCs and RPC replies, and when you send concurrent RPCs, the network can re-order requests and replies. Figure 2 is pretty good about pointing out places where RPC handlers have to be careful about this (e.g. an RPC handler should ignore RPCs with old terms). Figure 2 is not always explicit about RPC reply processing. The leader has to be careful when processing replies; it must check that the term hasn't changed since sending the RPC, and must account for the possibility that replies from concurrent RPCs to the same follower have changed the leader's state (e.g. nextIndex).

请记住，网络可以延迟RPC和RPC响应，并且当你发送并发的RPC时，网络可以重新排列请求和响应的顺序。图2很好地指出了RPC处理程序必须注意的地方(例如，RPC处理程序应该忽略带有旧任期的RPC)。图2并不总是显式地说明RPC响应处理。Leader在处理回复时必须小心；它必须检查任期自从发送RPC以来没有改变，并且必须考虑并发RPC对同一follower的回复改变了leader状态的可能性(例如nextIndex)。