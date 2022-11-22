# Raft Locking Advice

If you are wondering how to use locks in the 6.824 Raft labs, here are some rules and ways of thinking that might be helpful.

如果你想知道如何在6.824 Raft lab中使用锁，这里有一些规则和思维方式，可能会有帮助。

Rule 1: Whenever you have data that more than one goroutine uses, and at least one goroutine might modify the data, the goroutines should use locks to prevent simultaneous use of the data. The Go race detector is pretty good at detecting violations of this rule (though it won't help with any of the rules below).

规则1：每当数据被一个以上的goroutine使用，并且至少有一个goroutine可能会修改该数据时，goroutine应该使用锁来防止并发地使用该数据。Go竞争检测器(race detector)在检测违反这一规则方面相当出色（尽管它对下面的任何规则都没有帮助）。

Rule 2: Whenever code makes a sequence of modifications to shared data, and other goroutines might malfunction if they looked at the data midway through the sequence, you should use a lock around the whole sequence.

规则2：每当代码对共享数据进行一连串的修改时，如果其他goroutine在中途查看数据可能会出现故障，你应该在整个序列周围使用锁。

An example:
```golang
  rf.mu.Lock()
  rf.currentTerm += 1
  rf.state = Candidate
  rf.mu.Unlock()
```

It would be a mistake for another goroutine to see either of these updates alone (i.e. the old state with the new term, or the new term with the old state). So we need to hold the lock continuously over the whole sequence of updates. All other code that uses rf.currentTerm or rf.state must also hold the lock, in order to ensure exclusive access for all uses.

如果另一个goroutine单独看到这些更新中的任何一个（即旧的状态和新的任期，或者新的任期和旧的状态），将会出错。所以我们需要在整个更新序列中持续持有锁。所有其他使用rf.currentTerm或rf.state的代码也必须持有该锁，以确保所有使用的独占性。

The code between Lock() and Unlock() is often called a "critical section." The locking rules a programmer chooses (e.g. "a goroutine must hold rf.mu when using rf.currentTerm or rf.state") are often called a "locking protocol".

Lock()和Unlock()之间的代码通常被称为 "关键部分"。程序员选择的持有锁的规则（例如，"一个goroutine在使用rf.currentTerm或rf.state时必须持有rf.mu"）通常被称为 "锁定协议"。

Rule 3: Whenever code does a sequence of reads of shared data (or reads and writes), and would malfunction if another goroutine modified the data midway through the sequence, you should use a lock around the whole sequence.

规则3：每当代码对共享数据进行一连串的读取（或读取和写入），并且如果另一个goroutine在中途修改了数据，就会发生故障，你应该在整个序列周围使用锁。

An example that could occur in a Raft RPC handler:
```golang
  rf.mu.Lock()
  if args.Term > rf.currentTerm {
   rf.currentTerm = args.Term
  }
  rf.mu.Unlock()
```

This code needs to hold the lock continuously for the whole sequence. Raft requires that currentTerm only increases, and never decreases. Another RPC handler could be executing in a separate goroutine; if it were allowed to modify rf.currentTerm between the if statement and the update to rf.currentTerm, this code might end up decreasing rf.currentTerm. Hence the lock must be held continuously over the whole sequence. In addition, every other use of currentTerm must hold the lock, to ensure that no other goroutine modifies currentTerm during our critical section.

这段代码需要在整个序列中持续保持锁。Raft要求currentTerm只能增加，而不能减少。另一个RPC处理程序可能在一个单独的goroutine中执行；如果允许它在if语句和更新到rf.currentTerm之间修改rf.currentTerm，这段代码可能最终会减少rf.currentTerm。因此，锁必须在整个序列中被持续持有。此外，对currentTerm的每一次使用都必须保持锁，以确保在我们的关键部分没有其他goroutine修改currentTerm。

Real Raft code would need to use longer critical sections than these examples; for example, a Raft RPC handler should probably hold the lock for the entire handler.

真正的Raft代码需要使用比这些例子更长的关键部分；例如，一个Raft RPC处理程序可能应该在整个处理程序中保持锁。

Rule 4: It's usually a bad idea to hold a lock while doing anything that might wait: reading a Go channel, sending on a channel, waiting for a timer, calling time.Sleep(), or sending an RPC (and waiting for the reply). One reason is that you probably want other goroutines to make progress during the wait. Another reason is deadlock avoidance. Imagine two peers sending each other RPCs while holding locks; both RPC handlers need the receiving peer's lock; neither RPC handler can ever complete because it needs the lock held by the waiting RPC call.

规则4：在做任何可能需要等待的事情时保持锁通常是个坏主意：读取Go通道，在通道上发送，等待计时器，调用time.Sleep()，或发送RPC（并等待回复）。一个原因是你可能希望其他goroutines在等待过程中继续运行。另一个原因是避免了死锁。想象一下，两个对等的节点在持有锁的同时互相发送RPC；两个RPC处理程序都需要接收对等节点的锁；两个RPC处理程序都不能完成，因为它需要等待的RPC调用所持有的锁。

Code that waits should first release locks. If that's not convenient, sometimes it's useful to create a separate goroutine to do the wait.

处于等待状态的代码应该首先释放锁。如果这不方便，有时创建一个单独的goroutine来进行等待是很有用的。

Rule 5: Be careful about assumptions across a drop and re-acquire of a lock. One place this can arise is when avoiding waiting with locks held. For example, this code to send vote RPCs is incorrect:

规则5：要小心假设跨越释放锁和重新获取锁可能会出现的问题。可能会出现的地方是当在持有锁的情况下避免等待。例如，这个发送投票RPC的代码是不正确的

```golang
  rf.mu.Lock()
  rf.currentTerm += 1
  rf.state = Candidate
  for <each peer> {
    go func() {
      rf.mu.Lock()
      args.Term = rf.currentTerm
      rf.mu.Unlock()
      Call("Raft.RequestVote", &args, ...)
      // handle the reply...
    } ()
  }
  rf.mu.Unlock()
```

The code sends each RPC in a separate goroutine. It's incorrect because args.Term may not be the same as the rf.currentTerm at which the surrounding code decided to become a Candidate. Lots of time may pass between when the surrounding code creates the goroutine and when the goroutine reads rf.currentTerm; for example, multiple terms may come and go, and the peer may no longer be a candidate. One way to fix this is for the created goroutine to use a copy of rf.currentTerm made while the outer code holds the lock. Similarly, reply-handling code after the Call() must re-check all relevant assumptions after re-acquiring the lock; for example, it should check that rf.currentTerm hasn't changed since the decision to become a candidate.

该代码在一个单独的goroutine中发送每个RPC。这是不正确的，因为args.Term可能与周围代码决定成为候选者的rf.currentTerm不一样。从周围的代码创建goroutine到goroutine读取rf.currentTerm之间可能会经过很多时间；例如，多个任期可能会来来去去，而节点可能不再是一个候选人。解决这个问题的一个方法是，创建的goroutine使用在外层代码持有锁的时候的rf.currentTerm的副本。同样，在Call()之后的响应处理代码必须在重新获得锁之后重新检查所有相关的假设；例如，它应该检查rf.currentTerm在决定成为候选人之后没有改变。

It can be difficult to interpret and apply these rules. Perhaps most puzzling is the notion in Rules 2 and 3 of code sequences that shouldn't be interleaved with other goroutines' reads or writes. How can one recognize such sequences? How should one decide where a sequence ought to start and end?

解释和应用这些规则可能很困难。也许最令人费解的是规则2和3中的代码序列概念，它不应该与其他goroutine的读或写交错。怎样才能识别这样的序列？如何决定一个序列应该在哪里开始和结束？

One approach is to start with code that has no locks, and think carefully about where one needs to add locks to attain correctness. This approach can be difficult since it requires reasoning about the correctness of concurrent code.

一种方法是，从没有锁的代码开始，仔细思考在哪里需要加锁以达到正确性。这种方法可能很困难，因为它需要对并发代码的正确性进行推理。

A more pragmatic approach starts with the observation that if there were no concurrency (no simultaneously executing goroutines), you would not need locks at all. But you have concurrency forced on you when the RPC system creates goroutines to execute RPC handlers, and because you need to send RPCs in separate goroutines to avoid waiting. You can effectively eliminate this concurrency by identifying all places where goroutines start (RPC handlers, background goroutines you create in Make(), &c), acquiring the lock at the very start of each goroutine, and only releasing the lock when that goroutine has completely finished and returns. This locking protocol ensures that nothing significant ever executes in parallel; the locks ensure that each goroutine executes to completion before any other goroutine is allowed to start. With no parallel execution, it's hard to violate Rules 1, 2, 3, or 5. If each goroutine's code is correct in isolation (when executed alone, with no concurrent goroutines), it's likely to still be correct when you use locks to suppress concurrency. So you can avoid explicit reasoning about correctness, or explicitly identifying critical sections.

一个更务实的方法是从观察开始的，如果没有并发（没有同时执行的goroutines），你就根本不需要锁。但是，当RPC系统创建goroutines来执行RPC处理程序时，你的并发性是被迫的，而且因为你需要在不同的goroutines中发送RPC以避免等待。你可以通过识别所有goroutine开始的地方（RPC处理程序，你在Make()中创建的后台goroutine，等等），在每个goroutine的最开始获得锁，并且只在该goroutine完全完成并返回时释放锁，来有效地消除这种并发性。这种锁协议确保了没有任何重要的东西是并行执行的；锁确保了每个goroutine在任何其他goroutine被允许启动之前执行完毕。由于没有并行执行，很难违反规则1、2、3或5。如果每个goroutine的代码在孤立情况下是正确的（当单独执行时，没有并发的goroutine），那么当你使用锁来抑制并发时，它可能仍然是正确的。因此，你可以避免对正确性进行明确的推理，或者明确地识别关键部分。

However, Rule 4 is likely to be a problem. So the next step is to find places where the code waits, and to add lock releases and re-acquires (and/or goroutine creation) as needed, being careful to re-establish assumptions after each re-acquire. You may find this process easier to get right than directly identifying sequences that must be locked for correctness.

然而，规则4很可能是一个问题。所以下一步是找到代码等待的地方，并根据需要增加锁的释放和重新获取（和/或goroutine的创建），注意在每次重新获取后重新建立假设。你可能会发现这个过程比直接确定必须锁定的序列更容易获得正确性。

(As an aside, what this approach sacrifices is any opportunity for better performance via parallel execution on multiple cores: your code is likely to hold locks when it doesn't need to, and may thus unnecessarily prohibit parallel execution of goroutines. On the other hand, there is not much opportunity for CPU parallelism within a single Raft peer.)

(作为一个旁观者，这种方法所牺牲的是通过多核上的并行执行来提高性能的任何机会：你的代码很可能在不需要的时候持有锁，因此可能不必要地禁止goroutine的并行执行。另一方面，在单个Raft节点中，也没有太多的机会实现CPU并行化。）