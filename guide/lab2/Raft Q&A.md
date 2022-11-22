# Raft Q&A -- Jon Gjengset

> For 6.824 we are using Piazza for class communication and Q&A. Over the course of the semester, a......

For 6.824 we are using [Piazza](https://piazza.com/class/igs35ab0zvja8) for class communication and Q&A. Over the course of the semester, a number of good questions have been asked that may be of use to others trying to come to grips with Raft. A selection of the questions and answers are given below. These are all adapted from the questions and answers given by 6.824 students and TAs.

This post accompanies the [Students’ Guide to Raft](https://thesquareplanet.com/blog/students-guide-to-raft/). You will probably want to read that first.

> Assume we have three servers, and that S0 is elected leader. S0 receives some commands from a client and adds them to its log (say, 1:100, 1:101, and 1:102, on the form term:command). S0 is then immediately partitioned from the rest of the servers before propagating those entries to any clients.
> 
> Next, S1 is elected leader right after the partitioning of S0. It gets two commands, 2:103 and 2:104, and replicates them to S2 (and thus also commits them). Immediately after this, S1 is partitioned off, and S0 is re-connected. S2 would now be elected leader, since logs on S0 are not up to date.
> 
> S0 would learn the commit index of S2 is 2 from its `AppendEntries`, but it should not commit any command from its log, because its log doesn’t match that of S2. If there is no new command from clients, when should we erase all conflicting log entries on S0?

You’re right that S0 will learn the new `leaderCommit` from S2’s heartbeat. But before any follower updates its `commitIndex` to match the leader’s, what does it need to do? Take another look at the order of instructions in the receiver implementation for the `AppendEntries` RPC in Figure 2. It’s very specific about when a follower’s conflicting log entries should be erased.

> In figure 8 of the raft paper: (d) seems bad because [2] has been committed, but gets rolled back. What are they saying prevents (d) from happening? What prevents S5 from being elected in step (d)?

[2] can’t be committed in term 4, because Raft never explicitly commits old entries (only implicitly by the Log Matching Property as explained in section 5.4.2). So in step (c), S1 would only be able to commit 2 if it were also able to commit 4, which would then exclude S5 from being an eligible candidate.

> When is current term supposed to be incremented? If my Raft instances are left idle (i.e., no client commands), they do not eventually agree on the current term. I’m incrementing the `currentTerm` field whenever I start an election, but I only do it for the candidate (sending out the votes). Is this correct, or should I be incrementing `currentTerm` for all the servers I send the request to?

You’re right that you should increment `currentTerm` when the current term times out and you start a new election. However, followers also have to update their terms at some point, or else you’ll end up with servers agreeing on the same leader, but for different terms. Just incrementing on all servers that you send the request to won’t work, because the voting servers might already have different terms, and might end up incrementing twice if they receive the same two `RequestVotes` for the same term, etc.

To figure out when and how you should update `currentTerm` (on all servers), see the rule for all servers in Figure 2 of the Raft paper: “If RPC request or response contains term `T > currentTerm`: set `currentTerm = T`, convert to follower.”

> I’m quite confused about the difference between the `RequestVote` RPC arguments and the `AppendEntries` RPC arguments. `RequestVote` has `lastLogIndex/Term`, while `AppendEntries` has `prevLogIndex/Term`. Are these equivalent?
> 
> I’m trying to mentally think of an example: If I have entries 0,1,2,3,4 in my log, then I’m assuming my `lastLogIndex` would be 4. But would `prevLogIndex` be the same thing? Does this differ for each follower you send it to? (i.e., do we use `nextIndex[]`/`matchIndex[]` to help determine `prevLogIndex`?)

When a candidate sends a `RequestVote` RPC, the `lastLogIndex` should be the index of its last log entry (so 5 in your example).

For `AppendEntries`, the `prevLogIndex/Term` should refer to the log entry immediately preceding the first element of the `entries[]` field of the RPC arguments in the leader’s log. Suppose the leader has log entries 0,1,2,3,4,5,6,7,8 and `nextIndex[i]` is 6 for some follower `i`. The leader wants to send entries 6,7,8 to that follower. The leader would copy 6,7,8 to `entries[]`, and set `prevLogIndex` to 5.

> What exactly is meant by “volatile state” in the Raft paper? Is this data lost if the server storing it crashes? If so, why are `commitIndex` and `lastApplied` volatile? Shouldn’t they be persistent?

Yes, “volatile” means it is lost if there’s a crash.

`commitIndex` is volatile because Raft can figure out a correct value for it after a reboot using just the persistent state. Once a leader successfully gets a new log entry committed, it knows everything before that point is also committed. A follower that crashes and comes back up will be told about the right `commitIndex` whenever the current leader sends it an `AppendEntries` RPC.

`lastApplied` starts at zero after a reboot because the Figure 2 design assumes the service (e.g., a key/value database) doesn’t keep any persistent state. Thus its state needs to be completely recreated by replaying all log entries. If the service does keep persistent state, it is expected to persistently remember how far in the log it has executed, and to ignore entries before that point. Either way it’s safe to start with `lastApplied = 0` after a reboot.

> According to Figure 2, persistent state is saved before responding to RPCs, but a leader never receive RPC from other raft servers when it’s working normally. This means if we only save persistent state when receiving `AppendEntries` or `RequestVote`, a leader never gets a chance to store persistent state, which is kind of weird… Or do the RPCs including ones called by clients?

For simplicity, you should save Raft’s persistent state just after any change to that state. The most important thing is that you save persistent state before you make it possible for anything else to _observe_ the new state, i.e., before you send an RPC, reply to an RPC, return from `Start()`, or apply a command to the state machine.

If a server changes persistent state, but then crashes before it gets the chance to save it, that’s fine – it’s as if the crash happened before the state was changed. However, if the server changes persistent state, _makes it visible_, and _then_ crashes before it saves it, that’s _not_ fine – forgetting that persistent state may cause it to violate protocol invariants (for example, it could vote for two different candidates in the same term if it forgot `votedFor`).

> Figure 2 says “if an existing entry conflicts with a new one”, delete it and everything that follows. But inside Section 5.3, it suggests we should find the latest log entry where the two logs agree.
> 
> What is the difference between Bullet point #2 and #3? Right now, I’m basically checking the value at `prevLogIndex+1`, and seeing if it’s equal to the leader’s term. If it isn’t, I delete it and everything following it. But based on 5.3, should I actually be going through the entire log from the end and checking if they agree?

I think you are just mixing up the roles of the follower and the leader in this case. The leader is essentially probing the follower’s log to find the last point where the two agree. This is what the `nextIndex` variable is used for. The follower helps the leader do this by a) rejecting any `AppendEntries` RPCs that doesn’t immediately follow a point where the two agree (this is #2), and b) overwriting any following entries in its log once #2 is satisfied (this is #3).

To put it simply, #2 makes sure that the entries before the ones contained in the `AppendEntries` RPC from the leader match on the leader and the follower. #3 ensures that the entries in the follower’s log following the prefix the leader and follower agree about are the same as the entries the leader holds in its log.

> I’m a little confused; what the difference is between a log entry that is “applied” and one that is “committed”. Are all applied entries committed, but not the other way around? When does a committed entry become applied?

Any log entry that you have applied to the application state machine is “applied”. An entry should never be applied unless it has already been committed. An entry can be committed, but not yet applied. You will likely apply committed entries very soon after they become committed.

> Why is it necessary to check if log[N].term == currentTerm? In the “Leaders” section of the “Rules for Servers” part of Figure 2, why is it necessary to only update `matchIndex` if `log[N].term == currentTerm`? What should happen if `log[N].term != currentTerm`?

Raft leaders can’t be sure an entry is actually committed (and will not ever be changed in the future) if it’s not from their current term, which Figure 8 from the paper illustrates. One way to think about it is that a follower only shows their “allegiance” to leader A by replicating a log record from A’s current term. If they haven’t, and a follower has only replicated a log entry from an earlier term, then another candidate B can come along with a conflicting entry in their log (same index but higher term) and “steal” the votes from a majority of such followers: B’s log is more-up-to-date than the followers by virtue of having a higher-termed entry in the last spot, so the followers have to vote for it. Then B, now a leader, overwrites that original log record with their own higher-termed one.

Note that this can’t happen if the follower replicates a log entry from A’s current term. In this case, since A’s current term is the highest at which a command was issued, a candidate’s log can only be more up-to-date than the follower’s if it includes that log entry, so any new candidate this follower votes for will never overwrite it.

> In `AppendEntries`, if the `prevLogTerm/Index` matches, I get rid of the log after the `prevlogIndex`, and just append the entries from the RPC arguments:
> 
> ```
> rf.Log = rf.Log[:args.PrevLogIndex + 1]
>   rf.Log = append(rf.Log, args.Entries ...)
> ```
> 
> However, what if the `AppendEntries` RPCs are received out-of-order?
> 
> Say that there are 3 machines, and S1 is leader. All this is in term 1.
> 
> ```
> S1: [C1]   S2: [C1]   S3: [C1]
> ```
> 
> *   S1 receives requests C2-5, making the logs:
>     
>     ```
>     S1: [C1,C2,C3,C4,C5]   S2: [C1]   S3: [C1]
>     ```
>     
> *   S1 sends out `AppendEntries` RPCs with:
>     
>     ```
>     {
>       prevLogIndex: 1,
>       prevLogTerm: 1,
>       entries: [C2, C3],
>     }
>     ```
>     
> *   S1 sends out `AppendEntries` RPCs with:
>     
>     ```
>     {
>       prevLogIndex: 1,
>       prevLogTerm: 1,
>       entries: [C2, C3, C4, C5],
>     }
>     ```
>     
>     This could happen if there’s a pause between leader receiving C3 and C4, and the other servers haven’t responded to the first RPC yet.
>     
> *   S1’s second `AppendEntries` RPC arrives, so we have:
>     
>     ```
>     S2: [C1,C2,C3,C4,C5]   S3: [C1,C2,C3,C4,C5]
>     ```
>     
>     They both respond positively.
>     
> *   S1 commits up to C5, and responds back to client.
> *   Now, S1’s first `AppendEntries` RPC arrives at S2 and S3. S2 and S3 revert back to `[C1,C2,C3]`.
> *   S1 crashes.
> *   **S2 can now become leader and the committed rule is broken!**
> 
> Why can this not happen in Raft?

This is a very good question. The answer lies in the exact wording of point #3 in the `AppendEntries` RPC definition in Figure 2 in the paper: “If an existing entry conflicts with a new one (same index but different terms), delete the existing entry and all that follow it.”

Note that the rule starts with “_if_ an existing entry conflicts”. This is important exactly for the scenario you outline above. Here’s what should happen + some intuition: There is a rule in Raft that the `commitIndex` can never be reduced (intuitively because we cannot un-apply a log entry). We know that if our `commitIndex` is ever changed so that it points to somewhere in the log, _all_ entries before that point will _never_ change ever again. Without the _if_ in the aforementioned rule from Figure 2, this rule would be violated, because, as you point out, it could cause a follower to uncommit entries that it has already committed.

The _if_ is what saves us. To see why, consider what happens if `prevLogIndex`’s term matches `prevLogTerm` (and the leader has an up-to-date term of course). This means that whatever the leader sent us _must_ be a prefix of the “true” log (that is, what `leaderCommit` applies to). This is true both for the second (“long”) `AppendEntries` RPC, and the first (“short”) `AppendEntries` RPC. It follows from this that the entries in the “short” RPC must be a prefix of those in the “long” RPC. A consequence of this is that we don’t match the if from the rule — no existing entry conflicts with a new one, and we should therefore _not_ truncate our log, but simply append to it. Now, we aren’t invalidating our old `commitIndex`, and all is well in the world.