// 表浏览器的查询结果缓存:表元数据(MySQL)与行数计数(MySQL/PG)。
// 两者均为 per-client 状态(随客户端生灭,不跨连接共享),mutex 保护、TTL
// 内命中直接复用;写路径(数据写/DDL/TRUNCATE)按表失效。键空间以「用户
// 浏览过的表 × where 变体」为界,无淘汰策略:过期条目在同键写入时覆盖、
// 在失效时删除,客户端关闭后整体随对象回收。kafka 等其他数据源不经过本文件。
package service

import (
	"strings"
	"sync"
	"time"

	"sheng-shou-yun-he/backend/internal/model"
)

// 浏览缓存的存活时间:翻页与 SQL 控制台在 TTL 内复用元数据/计数。包内变量
// 便于测试收缩(置为负值即全部条目立即过期,确定性触发回源)。
var (
	browseMetaTTL  = 30 * time.Second
	browseCountTTL = 30 * time.Second
)

// --- 表元数据缓存(MySQL:engine + 列 + 主键) ---

// mysqlTableMetaEntry 缓存一表的 engine 与列/主键元数据(同源于
// information_schema.tables 与 information_schema.columns 的查询结果)。
type mysqlTableMetaEntry struct {
	engine  string
	cols    []model.MysqlColumn
	pk      []string
	fetched time.Time
}

// mysqlTableMetaCache 是 MysqlClient 的表元数据缓存,键为
// mysqlBrowseMetaKey(database, table)。零值即可用(entries 惰性初始化)。
type mysqlTableMetaCache struct {
	mu      sync.Mutex
	entries map[string]mysqlTableMetaEntry
}

// mysqlBrowseMetaKey 拼装元数据缓存键:\x00 分隔,标识符内不可能出现,
// 规避 "a.b"+"c" 与 "a"+"b.c" 的拼接歧义。
func mysqlBrowseMetaKey(database, table string) string {
	return database + "\x00" + table
}

// get 返回未过期的条目;命中结果里的切片是深拷贝,调用方与缓存互不共享
// 底层数组。
func (c *mysqlTableMetaCache) get(key string, ttl time.Duration) (mysqlTableMetaEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok || time.Since(e.fetched) >= ttl {
		return mysqlTableMetaEntry{}, false
	}
	return mysqlTableMetaEntry{
		engine: e.engine,
		cols:   cloneMysqlColumns(e.cols),
		pk:     cloneStrings(e.pk),
	}, true
}

// put 整条覆盖。只在未命中(缺失或已过期)后才会走到,不存在覆盖更新数据
// 的窗口;存入切片同样深拷贝,调用方后续修改不影响缓存。
func (c *mysqlTableMetaCache) put(key string, e mysqlTableMetaEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[string]mysqlTableMetaEntry{}
	}
	c.entries[key] = mysqlTableMetaEntry{
		engine:  e.engine,
		cols:    cloneMysqlColumns(e.cols),
		pk:      cloneStrings(e.pk),
		fetched: time.Now(),
	}
}

// setEngine 为既有条目补 engine(SQL 控制台的列元数据路径先建的条目不带
// engine,PageRows 命中后补拉回填);无条目时不创建。
func (c *mysqlTableMetaCache) setEngine(key, engine string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok {
		return
	}
	e.engine = engine
	c.entries[key] = e
}

// drop 清掉一表的元数据条目(DDL/TRUNCATE 后失效)。
func (c *mysqlTableMetaCache) drop(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, key)
}

// --- 行数计数缓存(MySQL + PG) ---

// browseCountEntry 缓存一表(一 where 变体)的行数计数。
type browseCountEntry struct {
	total   int64
	fetched time.Time
}

// browseCountCache 是 per-client 的行数计数缓存,键由调用方按
// <库名>\x00<表名>\x00<规范化 where> 拼装。零值即可用。
type browseCountCache struct {
	mu      sync.Mutex
	entries map[string]browseCountEntry
}

// get 返回未过期的计数。
func (c *browseCountCache) get(key string, ttl time.Duration) (int64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok || time.Since(e.fetched) >= ttl {
		return 0, false
	}
	return e.total, true
}

// put 写入计数(同键覆盖,即未命中回源后的常规写入)。
func (c *browseCountCache) put(key string, total int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[string]browseCountEntry{}
	}
	c.entries[key] = browseCountEntry{total: total, fetched: time.Now()}
}

// dropPrefix 清掉前缀匹配的全部条目(一表所有 where 变体的计数一并失效)。
func (c *browseCountCache) dropPrefix(prefix string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k := range c.entries {
		if strings.HasPrefix(k, prefix) {
			delete(c.entries, k)
		}
	}
}

// --- 键拼装与切片深拷贝 ---

// normalizeBrowseWhere 归一化计数键里的 where:仅 TrimSpace(空 where 也是
// 合法键,与「无筛选」语义一一对应)。
func normalizeBrowseWhere(where string) string {
	return strings.TrimSpace(where)
}

// mysqlBrowseCountKey 拼装 MySQL 行数计数的缓存键。
func mysqlBrowseCountKey(database, table, where string) string {
	return database + "\x00" + table + "\x00" + normalizeBrowseWhere(where)
}

// mysqlBrowseCountPrefix 返回一表全部 where 变体计数键的公共前缀。
func mysqlBrowseCountPrefix(database, table string) string {
	return database + "\x00" + table + "\x00"
}

// postgresBrowseCountKey 拼装 PG 行数计数的缓存键(database 为 getDB 归一后
// 的实际池名;PG 无三段命名,库只用来选池,但同一客户端跨多库池化,键里
// 必须带上才能互不串扰)。
func postgresBrowseCountKey(database, schema, relation, where string) string {
	return database + "\x00" + schema + "\x00" + relation + "\x00" + normalizeBrowseWhere(where)
}

// postgresBrowseCountPrefix 返回一关系全部 where 变体计数键的公共前缀。
func postgresBrowseCountPrefix(database, schema, relation string) string {
	return database + "\x00" + schema + "\x00" + relation + "\x00"
}

// cloneMysqlColumns 深拷贝列元数据切片(MysqlColumn 全为值字段,浅拷贝
// 切片即可隔离)。
func cloneMysqlColumns(cols []model.MysqlColumn) []model.MysqlColumn {
	if cols == nil {
		return nil
	}
	out := make([]model.MysqlColumn, len(cols))
	copy(out, cols)
	return out
}

// cloneStrings 深拷贝字符串切片(主键列名)。
func cloneStrings(s []string) []string {
	if s == nil {
		return nil
	}
	out := make([]string, len(s))
	copy(out, s)
	return out
}
