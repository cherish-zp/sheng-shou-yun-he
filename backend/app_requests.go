// 各 SQL 数据源共享的 App 请求载荷。CH 与 MySQL/TiDB 的分页请求、CH 与 ES 的
// 控制台执行请求、MySQL/TiDB 与 Hive 的带库上下文执行请求在字段与 json tag
// 上逐字一致,合并于此消除复制;各数据源绑定文件用类型别名保持原有导出名,
// 方法签名与 wire 兼容性不受影响(json tag 逐字保留,前端无需重新生成 wailsjs)。
package backend

// PageRowsRequest carries one page request over a table. 被 CH 与 MySQL/TiDB
// 的分页绑定共用(原两源结构逐字段一致)。
type PageRowsRequest struct {
	ConnectionID string `json:"connection_id"`
	Database     string `json:"database"`
	Table        string `json:"table"`
	// Where is the user's native WHERE fragment (without the keyword); empty
	// omits the clause.
	Where   string `json:"where,omitempty"`
	OrderBy string `json:"order_by,omitempty"`
	Asc     bool   `json:"asc"`
	Limit   int    `json:"limit"`
	Offset  int    `json:"offset"`
}

// ExecuteRequest carries the multi-statement SQL script. Limit/Offset 启用
// 服务端分页:Limit>0 时仅返回 Offset 起的 Limit 行,结果按语句类型附
// total_rows(≥0 精确 / -1 无法计数)。被 CH 与 ES 的控制台绑定共用。
type ExecuteRequest struct {
	ConnectionID string `json:"connection_id"`
	SQL          string `json:"sql"`
	Limit        int    `json:"limit,omitempty"`
	Offset       int    `json:"offset,omitempty"`
}

// DatabaseExecuteRequest carries the multi-statement SQL script plus the
// console's current database context: non-empty runs the whole script on one
// connection pinned to it via USE; empty keeps the pool default (no USE).
// Limit/Offset 语义同 ExecuteRequest。被 MySQL/TiDB 与 Hive 的控制台绑定共用
// (两源原字段 json tag 一致,仅声明顺序不同;JSON 对象无序,wire 等价)。
type DatabaseExecuteRequest struct {
	ConnectionID string `json:"connection_id"`
	Database     string `json:"database,omitempty"`
	SQL          string `json:"sql"`
	Limit        int    `json:"limit,omitempty"`
	Offset       int    `json:"offset,omitempty"`
}
