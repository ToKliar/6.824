# SOFAJRaft 日志复制 - pipeline实现剖析 | SOFAJRaft 实现原理

> SOFAStack（Scalable Open Financial Architecture Stack） 是蚂蚁金服自主研发的金融级分布式架构，包含了构建金融级云原生架构所需的各个组件，是在金融场景里锤炼出来的最佳实践。

![](https://mmbiz.qpic.cn/mmbiz_png/nibOZpaQKw0icSopE8dboCY4pWXeusPVwaQiaU5hmJ5gSNC6rcp99yib4th0qkjtZcoxgXfbgfyXEC2XO0dywnmAAA/640?wx_fmt=png)  

SOFAJRaft 是一个基于 Raft 一致性算法的生产级高性能 Java 实现，支持 MULTI-RAFT-GROUP，适用于高负载低延迟的场景。

本文为《剖析 | SOFAJRaft 实现原理》第六篇，本篇作者徐家锋，来自专伟信息，力鲲，来自蚂蚁金服。《剖析 | SOFAJRaft 实现原理》系列由 SOFA 团队和源码爱好者们出品，项目代号：<SOFA:JRaftLab/>，文章尾部有参与方式，欢迎同样对源码热情的你加入。

SOFAJRaft：

https://github.com/sofastack/sofa-jraft

本文的目的是要介绍 SOFAJRaft 在日志复制中所采用的 pipeline 机制，但是作者落笔时突然觉得这个题目有些唐突，我们不应该假设读者理所应当的对日志复制这个概念已经了然于胸，所以作为一篇解析，我觉得还是应该先介绍一下 SOFAJRaft 中的日志复制是要解决什么问题。

**概念介绍** 

SOFAJRaft 是对 Raft 共识算法的 Java 实现。既然是共识算法，就不可避免的要对需要达成共识的内容在多个服务器节点之间进行传输，在 SOFAJRaft 中我们将这些内容封装成一个个日志块 (LogEntry)，这种服务器节点间的日志传输行为在 SOFAJRaft 中也就有了专门的术语：**日志复制**。

为了便于阅读理解，我们用一个象棋的故事来类比日志复制的流程和可能遇到的问题。

假设我们穿越到古代，要为一场即将举办的象棋比赛设计直播方案。当然所有电子通讯技术此时都已经不可用了，幸好象棋比赛是一种能用精简的文字描述赛况的项目，比如：“炮二平五”, “马８进７”, “车２退３” 等，我们将这些描述性文字称为**棋谱**。这样只要我们在场外同样摆上棋盘 (可能很大，方便围观)，通过棋谱就可以把棋手的对弈过程直播出来。

![](https://mmbiz.qpic.cn/mmbiz_png/nibOZpaQKw0icSopE8dboCY4pWXeusPVwaGlze72PjWGG0FCEx5HEdGoIDAHzjAIvs206MeqdqwdTY2PTzohhzWQ/640?wx_fmt=png)

图 1 - 通过棋谱直播

所以我们的直播方案就是：赛场内两位棋手正常对弈，设一个专门的记录员来记录棋手走出的每一步，安排一个旗童飞奔于赛场内外，棋手每走一步，旗童就将其以棋谱的方式传递给场外，这样观众就能在场外准实时的观看对弈的过程，获得同观看直播相同的体验。

![](https://mmbiz.qpic.cn/mmbiz_png/nibOZpaQKw0icSopE8dboCY4pWXeusPVwaxer5Bynsib21sQs0B2MjAy90eO6fmVMPuVmPLefjMPdVibPp5l1WFmvg/640?wx_fmt=png)

图 2 - 一个简单的直播方案

这便是 SOFAJRaft 日志复制的人肉版，接下来我们完善一下这个 “直播系统”，让它逐步对齐真实的日志复制。

**改进 1. 增加记录员的数量** 

假设我们的比赛获得了很高的关注度，我们需要在赛场外摆出更多的直播场地以供更多的观众观看。

![](https://mmbiz.qpic.cn/mmbiz_png/nibOZpaQKw0icSopE8dboCY4pWXeusPVwaRx5icjJB6cWolLib72K14JZ8DxmoIE1DXa4IkP0tpOWGXZOA0t1usxbw/640?wx_fmt=png)

图 3 - 更多的直播平台

这样我们就要安排更多的旗童来传递棋谱，场外的每一台直播都需要一个旗童来负责，这些旗童不停的在赛场内外奔跑传递棋谱信息。有的直播平台离赛场远一些，旗童要跑很久才行，相应的直播延迟就会大一些，而有些直播平台离得很近，对应的旗童就能很快的将对弈情况同步到直播。

随着直播场地的增加，负责记录棋局的记录员的压力就会增加，因为他要针对不同的旗童每次提供不同的棋谱内容，有的慢有的快。如果记录员一旦记混了或者眼花了，就会出现严重的直播事故 (观众看到的不再是棋手真正的棋局)。

![](https://mmbiz.qpic.cn/mmbiz_png/nibOZpaQKw0icSopE8dboCY4pWXeusPVwaHzotS5qoF0MAsKbJEUCeI8GYf1u8KfSicSxtQ9icKxlwHR0HDILwusHg/640?wx_fmt=png)

图 4 - 压力很大的记录员

为此我们要作出一些优化，为每个场外的直播平台安排一个专门的记录员，这样 “赛局 - 记录员 - 旗童 - 直播局” 就构成了单线模式，专人专职高效可靠。

![](https://mmbiz.qpic.cn/mmbiz_png/nibOZpaQKw0icSopE8dboCY4pWXeusPVwawvjQVln2YndMcKCHM7BJCO4EtVXO9LNOicnQ2cGWoJ9fZnRtkr3WERA/640?wx_fmt=png)

图 5 - “赛局 - 记录员 - 旗童 - 直播棋局”

**改进 2. 增加旗童每次传递的信息量** 

起初我们要求棋手每走一步，旗童就向外传递一次棋谱。可是随着比赛进行，其弊端也逐渐显现，一方面记录员记录了很多棋局信息没有传递出去，以至于不得不请求棋手停下来等待 (不可思议)；另一方面，场外的观众对于这种 “卡帧” 的直播模式也很不满意。

所以我们做出改进，要求旗童每次多记几步棋，这样记录员不会积攒太多的待直播信息，观众也能一次看到好几步，而这对于聪明的旗童来说并不是什么难事，如此改进达到了共赢的局面。

![](https://mmbiz.qpic.cn/mmbiz_png/nibOZpaQKw0icSopE8dboCY4pWXeusPVwaMic8NOIBrvs3eK4oeH8aicqIWXMKywa4ezbbl5SdJnCseogMwluvWeibg/640?wx_fmt=png)

图 6 - 旗童批量携带信息

**改进 3. 增加快照模式** 

棋局愈发精彩，应棋迷的强烈要求，我们临时增加了几个直播场地，这时棋手已经走了很多步了，按照我们的常规手段，负责新直播的记录员和旗童需要把过去的每一步都在直播棋盘上还原一遍 (回放的过程)，与此同时棋手还在不断下出新的内容。

从直觉上来说这也是一种很不聪明的方式，所以这时我们采用快照模式，不再要求旗童传递过去的每一步棋谱，而是把当前的棋局图直接描下来，旗童将图带出去后，按照图谱直接摆子。这样新直播平台就能快速追上棋局进度，让观众欣赏到赛场同步的棋局对弈了。

![](https://mmbiz.qpic.cn/mmbiz_png/nibOZpaQKw0icSopE8dboCY4pWXeusPVwah6n6Rm8VicUWVcSkwmfmnJ6yksgrkscb2OY2d0wkJhvsRE2rwuSRJvw/640?wx_fmt=png)

图 7 - 采用快照模式

**改进 4. 每一个直播平台用多个旗童传递信息**

虽然我们之前已经在改进 2 中增加了旗童每次携带的信息量，但是在一些情况下 (棋手下快棋、直播平台很远等)，记录员依然无法将信息及时同步给场外。这时我们需要增加多个旗童，各旗童有次序的将信息携带到场外，这样记录员就可以更快速的把信息同步给场外直播平台。

![](https://mmbiz.qpic.cn/mmbiz_png/nibOZpaQKw0icSopE8dboCY4pWXeusPVwar5trbeohGRl9b5wMBvsNneaibV0aEiactG4G2IicN7PPf3pMIicOoPnDrw/640?wx_fmt=png)

图 8 - 利用多个旗童传递信息，实现 pipeline 效果

现在这个人肉的直播平台在我们的逐步改进下已经具备了 SOFAJRaft 日志复制的下面几个主要特点：  

**特点 1：被复制的日志是有序且连续的** 

如果棋谱传递的顺序不一样，最后下出的棋局可能也是完全不同的。而 SOFAJRaft 在日志复制时，其日志传输的顺序也要保证严格的顺序，所有日志既不能乱序也不能有空洞 (也就是说不能被漏掉)。

![](https://mmbiz.qpic.cn/mmbiz_png/nibOZpaQKw0icSopE8dboCY4pWXeusPVwa7zwQ3q3THXK1aPrhsQUjNrCROcQkUxBP3twUHAq5bb79QA3enZl4gw/640?wx_fmt=png)

图 9 - 日志保持严格有序且连续

**特点 2：复制日志是并发的** 

SOFAJRaft 中 Leader 节点会同时向多个 Follower 节点复制日志，在 Leader 中为每一个 Follower 分配一个 Replicator，专用来处理复制日志任务。在棋局中我们也针对每个直播平台安排一个记录员，用来将对弈棋谱同步给对应的直播平台。

![](https://mmbiz.qpic.cn/mmbiz_png/nibOZpaQKw0icSopE8dboCY4pWXeusPVwaYs31P81HksATNOywm6duVzN3TxlX6oosjbUDcBcjQsC0fuvicceFqug/640?wx_fmt=png)

图 10 - 并发复制日志

**特点 3：复制日志是批量的** 

SOFAJRaft 中 Leader 节点会将日志成批的复制给 Follower，就像旗童会每次携带多步棋信息到场外。

![](https://mmbiz.qpic.cn/mmbiz_png/nibOZpaQKw0icSopE8dboCY4pWXeusPVwaLxrXiaYicUzMmVCSeXzk0wc80EmjkeMnYYtEDesTEyDAIeAmQxdMNheg/640?wx_fmt=png)

图 11 - 日志被批量复制

**特点 4：日志复制中的快照** 

在改进 3 中，我们让新加入的直播平台直接复制当前的棋局，而不再回放过去的每一步棋谱，这就是 SOFAJRaft 中的快照 (Snapshot) 机制。用 Snapshot 能够让 Follower 快速跟上 Leader 的日志进度，不再回放很早以前的日志信息，即缓解了网络的吞吐量，又提升了日志同步的效率。

**特点 5：复制日志的 pipeline 机制** 

在改进 4 中，我们让多个旗童参与信息传递，这样记录员和直播平台间就可以以 “流式” 的方式传递信息，这样既能保证信息传递有序也能保证信息传递持续。

在 SOFAJRaft 中我们也有类似的机制来保证日志复制流式的进行，这种机制就是 pipeline。Pipeline 使得 Leader 和 Follower 双方不再需要严格遵从 “Request - Response - Request” 的交互模式，Leader 可以在没有收到 Response 的情况下，持续的将复制日志的 AppendEntriesRequest 发送给 Follower。

在具体实现时，Leader 只需要针对每个 Follower 维护一个队列，记录下已经复制的日志，如果有日志复制失败的情况，就将其后的日志重发给 Follower。这样就能保证日志复制的可靠性，具体细节我们在源码解析中再谈。

![](https://mmbiz.qpic.cn/mmbiz_png/nibOZpaQKw0icSopE8dboCY4pWXeusPVwaTVzo7AO1qHo5vPVzZH2Ak9nA5ZuH9sV6a93l905NicktZsZSAQHZ9Pw/640?wx_fmt=png)

图 12 - 日志复制的 pipeline 机制

**源码解析** 

上面就是日志复制在原理层面的介绍，而在代码实现中主要是由 `Replicator` 和 `NodeImpl` 来分别实现 Leader 和 Follower 的各自逻辑，主要的方法列于下方。在处理源码中有三点值得我们关注。

![](https://mmbiz.qpic.cn/mmbiz_png/nibOZpaQKw0icSopE8dboCY4pWXeusPVwaT1xlBkg49XYrhsmK39TgC6ZoEibKCpO8LLMibdjCiavhfpq5XEFToxLQA/640?wx_fmt=png)

图 13 - 相关的方法

### **关注 1: Replicator 的 Probe 状态**

![](https://mmbiz.qpic.cn/mmbiz_png/nibOZpaQKw0icSopE8dboCY4pWXeusPVwac4SIQSl9yicQictYib8bGoCdYH6S5MWJHicIJCZN8NQ7hyA9whwtcOAZVQ/640?wx_fmt=png)

图 14 - Replicator 的状态

Leader 节点在通过 Replicator 和 Follower 建立连接之后，要发送一个 Probe 类型的探针请求，目的是知道 Follower 已经拥有的的日志位置，以便于向 Follower 发送后续的日志。

![](https://mmbiz.qpic.cn/mmbiz_png/nibOZpaQKw0icSopE8dboCY4pWXeusPVwaky0hl75fF8bavkX5MwfQKrgLqKPUhH2yPF5xH4e6SAgwvuiabElVejw/640?wx_fmt=png)

图 15 - 发送探针来知道 follower 的 logindex

### 关注 2: 用 Inflight 来辅助实现 pipeline

Inflight 是对批量发送出去的 logEntry 的一种抽象，他表示哪些 logEntry 已经被封装成日志复制 request 发送出去了。

![](https://mmbiz.qpic.cn/mmbiz_png/nibOZpaQKw0icSopE8dboCY4pWXeusPVwaeYjP7fnwicztic56ZntEDCIp4K94WyJXSdCUpTFibETsPZeV2k1uzI8sQ/640?wx_fmt=png)

图 16 - Inflight 结构

Leader 维护一个 queue，每发出一批 logEntry 就向 queue 中 添加一个代表这一批 logEntry 的 Inflight，这样当它知道某一批 logEntry 复制失败之后，就可以依赖 queue 中的 Inflight 把该批次 logEntry 以及后续的所有日志重新复制给 follower。既保证日志复制能够完成，又保证了复制日志的顺序不变。

这部分从逻辑上来说比较清晰，但是代码层面需要考虑的东西比较多，所以我们在此处贴出源码，读者可以在源码中继续探索。

![](https://mmbiz.qpic.cn/mmbiz_png/nibOZpaQKw0icSopE8dboCY4pWXeusPVwaYsXncZKJcPibBR0OZMEP0pbrX1smnjsIvCnUkWGFsoWDiblk4WuqibRLQ/640?wx_fmt=png)

图 17 - 复制日志的主要方法

![](https://mmbiz.qpic.cn/mmbiz_png/nibOZpaQKw0icSopE8dboCY4pWXeusPVwaRMF7EklMVgLmIcMoXC7YHXHBlw2jcSJ83jKqTow3UDaXpzHd74rx5w/640?wx_fmt=png)

图 18 - 添加 Inflight 到队列中

当然在日志复制中其实还要考虑更加复杂的情况，比如一旦发生切换 leader 的情况，follower 该如何应对，这些问题希望大家能够进入源码来寻找答案。

### 关注 3: 通信层采用单线程 & 单链接

在 pipeline 机制中，虽然我们在 SOFAJRaft 层面通过 Inflight 队列保证了日志是被有序的复制，对于乱序传输的 LogEntry 通过各种异常流程去排除掉，但是这些被排除掉的乱序日志最终还是要通过重传来保证最终成功，这就会影响日志复制的效率。

![](https://mmbiz.qpic.cn/mmbiz_png/nibOZpaQKw0icSopE8dboCY4pWXeusPVwajCwLziabagWbJsd6YBmCIwibHib8uiaiaJPg13A5gfTzludPrl9gs8PRLoQ/640?wx_fmt=png)

图 19 - 通信层不能保证有序

如上图所示，发送端的 Connection Pool 和 接收端的 Thread Pool 都会让原本 “单行道” 上有序传输的日志进入“多车道”，因而无法保证有序。所以在通信层面 SOFAJRaft 做了两部分优化去尽量保证 LogEntry 在传输中不会乱序。

1. 在 Replicator 端，通过 uniqueKey 对日志传输所用的 Url 进行特殊标识 ，这样 SOFABolt (SOFAJRaft 底层所采用的通信框架) 就会为这种 Url 建立单一的连接，也就是发送端的 Connection Pool 中只有一条可用连接。

![](https://mmbiz.qpic.cn/mmbiz_png/nibOZpaQKw0icSopE8dboCY4pWXeusPVwa8ibFg3MDWCHj8Y8Y3ZhloqE59jeOXI5Is5O1EuNymLCtjf0QWeiazqYw/640?wx_fmt=png)

图 20 - 通过 uniqueKey 定制 Url

2. 在接收端不采用线程池派发任务，增加判断 _dispatch_msg_list_in_default_executor_ 使得我们可以通过 io 线程直接将任务投递到 Processor 中。我们对 SOFABolt 做过一些功能增强，这里提供相关 PR #84 ，有兴趣的读者可以前往了解。

PR #84：

https://github.com/sofastack/sofa-bolt/pull/84

![](https://mmbiz.qpic.cn/mmbiz_png/nibOZpaQKw0icSopE8dboCY4pWXeusPVwahHl6Zicxx9bOypbMUDibvDQ4vHQwsf3rXIZicS7z8aLHsEHOs3EVGPnkA/640?wx_fmt=png)  

图 21 - SOFABolt 利用 IO 线程派发 AppendEntriesRequest 到 Processor

这样日志复制的通信模型就变成了我们期望的 “单行道” 的模式。这种 “单行道” 能够很大程度上保证传输的日志是有序且连续的，从而提升了 pipeline 的效率。

![](https://mmbiz.qpic.cn/mmbiz_png/nibOZpaQKw0icSopE8dboCY4pWXeusPVwa8MvicbFicje4vNOVaob2B2xSU4n8wIHLBPW7wzT0Wtv4Dg7y90y31gCQ/640?wx_fmt=png)

图 22 - 优化通信模型

**总结** 

日志复制并不是一个复杂的概念，pipeline 机制也是一种符合直觉思维的优化方式，甚至在我们的日常生活中也能找到这些概念的实践。在 SOFAJRaft 中，日志复制的真正难点是如何在分布式环境下既考虑到各种细节和异常，又保证高性能。本文只是从概念上尝试介绍了日志复制，更多的细节还需读者进入代码去寻找答案。

### SOFAJRaft 源码解析系列阅读

*   [SOFAJRaft-RheaKV MULTI-RAFT-GROUP 实现分析 | SOFAJRaft 实现原理](http://mp.weixin.qq.com/s?__biz=MzUzMzU5Mjc1Nw==&mid=2247485303&idx=1&sn=376da729dc166aa81f4f0732b2fbda31&chksm=faa0e8adcdd761bb1d05b46f429c0fc87c8994038ca9f2a69531a626d4577fdaa46fdccf99e9&scene=21#wechat_redirect)
    
*   [SOFAJRaft 选举机制剖析 | SOFAJRaft 实现原理](http://mp.weixin.qq.com/s?__biz=MzUzMzU5Mjc1Nw==&mid=2247485265&idx=1&sn=2e905b8c018e64baa72c8641049e67b1&chksm=faa0e88bcdd7619d09f7d8c67b67f85296a493f3943de143c00e482ae30b82174345cb2f9e79&scene=21#wechat_redirect)
    
*   [SOFAJRaft 线性一致读实现剖析 | SOFAJRaft 实现原理](http://mp.weixin.qq.com/s?__biz=MzUzMzU5Mjc1Nw==&mid=2247485218&idx=1&sn=17b0db520c2b2c04b947e1eccb704ca1&chksm=faa0e8f8cdd761ee7e4ad1f6765cf91d3aa5f65c797ca2660674ce0c6aafbaeab52db37256e5&scene=21#wechat_redirect)
    
*   [SOFAJRaft-RheaKV 是如何使用 Raft 的 | SOFAJRaft 实现原理](http://mp.weixin.qq.com/s?__biz=MzUzMzU5Mjc1Nw==&mid=2247485068&idx=1&sn=8dc0913c47a7a0f76424aaea35ab538a&chksm=faa0e956cdd7604018b53f2749b734faa71a2748ab6b248a7eb80ebd98003706a140aad9c9f2&scene=21#wechat_redirect)
    
*   [蚂蚁金服生产级 Raft 算法库 SOFAJRaft 存储模块剖析 | SOFAJRaft 实现原理](http://mp.weixin.qq.com/s?__biz=MzUzMzU5Mjc1Nw==&mid=2247485000&idx=1&sn=42b6f967b2ad43dd82983929d5800a33&chksm=faa0e992cdd7608499b5d58a65334653059acc2e35381157724c55d6a50743ba024298c63384&scene=21#wechat_redirect)  
    

本文归档在 sofastack.tack 网站，点击 “阅读原文” 即可查看。

![](https://mmbiz.qpic.cn/mmbiz_png/nibOZpaQKw0icibicOqL7gsNDpLictEiadM6SbFbzvS19lxehpYZ6AsX3bpqPlrb4EDicFBCts2uIjWCQn81fHhibZutbQ/640?wx_fmt=png)