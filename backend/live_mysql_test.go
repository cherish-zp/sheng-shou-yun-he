package backend

import (
	"context"
	"os"
	"strconv"
	"testing"

	"sheng-shou-yun-he/backend/internal/model"
	"sheng-shou-yun-he/backend/internal/service"
)

// TestLiveMysqlPageRowsPK 连真实 MySQL/TiDB 验证「有主键表的 primary_key 元
// 数据」。默认跳过;设置 MSMS_LIVE_MYSQL=1 才运行,端口/库可用
// MSMS_LIVE_MYSQL_PORT(默认 13306)、MSMS_LIVE_MYSQL_DB(默认 msms)、
// MSMS_LIVE_MYSQL_PASS(默认空,TiDB 默认无密码)覆盖。
func TestLiveMysqlPageRowsPK(t *testing.T) {
	if os.Getenv("MSMS_LIVE_MYSQL") == "" {
		t.Skip("未设置 MSMS_LIVE_MYSQL,跳过真实库验证")
	}
	port := 13306
	if p := os.Getenv("MSMS_LIVE_MYSQL_PORT"); p != "" {
		if n, err := strconv.Atoi(p); err == nil {
			port = n
		}
	}
	dbName := os.Getenv("MSMS_LIVE_MYSQL_DB")
	if dbName == "" {
		dbName = "msms"
	}
	pass := os.Getenv("MSMS_LIVE_MYSQL_PASS")
	cfg := model.MysqlConfig{Host: "127.0.0.1", Port: port, Username: "root", Password: pass, Database: dbName}
	c, err := service.NewMysqlClient(cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer c.Close()
	res, err := c.PageRows(context.Background(), dbName, "msms_silent_config", "", "", true, 10, 0)
	if err != nil {
		t.Fatalf("PageRows: %v", err)
	}
	if len(res.PrimaryKey) == 0 {
		t.Fatalf("primary_key 为空!columns=%d engine=%s", len(res.Columns), res.Engine)
	}
	t.Logf("primary_key=%v engine=%s cols=%d", res.PrimaryKey, res.Engine, len(res.Columns))
}
