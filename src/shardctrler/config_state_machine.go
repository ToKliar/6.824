package shardctrler

import (
	"sort"
)

type MemoryConfig struct {
	Configs []Config
}

func NewMemoryConfig() *MemoryConfig {
	mc := new(MemoryConfig)
	mc.Configs = make([]Config, 1)
	mc.Configs[0].Groups = map[int][]string{}
	return mc
}

func (mc *MemoryConfig) Query(num int) (Config, Err) {
	if num < 0 || num >= len(mc.Configs) {
		return mc.Configs[len(mc.Configs) - 1], OK
	}
	return mc.Configs[num], OK
}

func (mc *MemoryConfig) Join(groups map[int][]string) Err {
	lastConfig := mc.Configs[len(mc.Configs)-1]
	newConfig := Config{len(mc.Configs), lastConfig.Shards, deepCopy(lastConfig.Groups)}
	for gid, servers := range groups {
		if _, ok := newConfig.Groups[gid]; !ok {
			newServers := make([]string, len(servers))
			copy(newServers, servers)
			newConfig.Groups[gid] = newServers
		}
	}

	g2s := group2Shards(newConfig)

	for {
		source, target := getGidWithMaxShards(g2s), getGidWithMinShards(g2s)
		if source != 0 && len(g2s[source]) - len(g2s[target]) <= 1 {
			break
		}
		g2s[target] = append(g2s[target], g2s[source][0])
		g2s[source] = g2s[source][1:]
	}

	var newShards [NShards]int

	for gid, shards := range g2s {
		for _, shard := range shards {
			newShards[shard] = gid
		}
	}

	newConfig.Shards = newShards
	mc.Configs = append(mc.Configs, newConfig)
	return OK
}

func (mc *MemoryConfig) Leave(gids []int) Err {
	lastConfig := mc.Configs[len(mc.Configs)-1]
	newConfig := Config{len(mc.Configs), lastConfig.Shards, deepCopy(lastConfig.Groups)}
	g2s := group2Shards(lastConfig)
	orphanShards := make([]int, 0)
	for _, gid := range gids {
		if _, ok := newConfig.Groups[gid]; ok {
			delete(newConfig.Groups, gid)
		}

		if shards, ok := g2s[gid]; ok {
			orphanShards = append(orphanShards, shards...)
			delete(g2s, gid)
		}
	}

	var newShards [NShards]int
	
	if len(newConfig.Groups) != 0 {
		for _, shard := range orphanShards {
			target := getGidWithMinShards(g2s)
			g2s[target] = append(g2s[target], shard)
		}

		for gid, shards := range g2s {
			for _, shard := range shards {
				newShards[shard] = gid
			}
		}
	}

	newConfig.Shards = newShards
	mc.Configs = append(mc.Configs, newConfig)
	return OK
}

func (mc *MemoryConfig) Move(shard int, gid int) Err {
	lastConfig := mc.Configs[len(mc.Configs)-1]
	newConfig := Config{len(mc.Configs), lastConfig.Shards, deepCopy(lastConfig.Groups)}
	newConfig.Shards[shard] = gid
	mc.Configs = append(mc.Configs, newConfig)
	return OK
}

func deepCopy(oldMap map[int][]string) map[int][]string {
	newMap := make(map[int][]string)
	for idx, value := range oldMap {
		newValue := make([]string, len(value))
		copy(newValue, value)
		newMap[idx] = newValue
	}
	return newMap
}

func group2Shards(config Config) map[int][]int {
	g2s := make(map[int][]int)
	for gid := range config.Groups {
		g2s[gid] = make([]int, 0)
	}
	for shardId, gid := range config.Shards {
		g2s[gid] = append(g2s[gid], shardId)
	}
	return g2s
}

func getGidWithMaxShards(g2s map[int][]int) int {
	if shards, ok := g2s[0]; ok && len(shards) > 0 {
		return 0;
	}

	gids := make([]int, 0)
	for key := range g2s {
		gids = append(gids, key)
	}

	sort.Ints(gids)

	max, index := -1, -1
	for _, gid := range gids {
		if gid != 0 && len(g2s[gid]) > max {
			max = len(g2s[gid])
			index = gid
		}
	}

	return index
}

func getGidWithMinShards(g2s map[int][]int) int {
	gids := make([]int, 0)
	for key := range g2s {
		gids = append(gids, key)
	}

	sort.Ints(gids)

	min, index := NShards + 1, -1
	for _, gid := range gids {
		if gid != 0 && len(g2s[gid]) < min {
			min = len(g2s[gid])
			index = gid
		}
	}

	return index
}