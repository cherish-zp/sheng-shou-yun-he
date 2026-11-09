package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"sheng-shou-yun-he/backend/internal/model"
)

// enableReadonly 打开生产安全模式并断言成功。
func enableReadonly(t *testing.T, s *Service, ctx context.Context) {
	t.Helper()
	if err := s.SetReadonlyMode(ctx, true); err != nil {
		t.Fatalf("enable readonly mode: %v", err)
	}
	if !s.IsReadonlyMode(ctx) {
		t.Fatal("IsReadonlyMode = false after enabling")
	}
}

// strPtr 是单元格值构造的小工具。
func strPtr(v string) *string { return &v }

// TestReadonlyModeRejectsWritesButReadonlyUnaffected 开启生产安全模式后:
// 变更类方法(CreateTopic/ProduceMessage/MysqlUpdateCell/CHUpdateCell/
// RedisDeleteKeys)在入口被拒绝;只读方法(ListTopics)不受影响照常返回。
// 写闸门位于池解析之前,拒绝不依赖任何已连接客户端。
func TestReadonlyModeRejectsWritesButReadonlyUnaffected(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	conn, err := svc.CreateConnection(ctx, sampleConn())
	if err != nil {
		t.Fatalf("create connection: %v", err)
	}
	enableReadonly(t, svc, ctx)

	const want = "生产安全模式已开启,写操作被拒绝"

	cases := []struct {
		name string
		run  func() error
	}{
		{"CreateTopic", func() error { return svc.CreateTopic(ctx, conn.ID, "t1", 1, 1) }},
		{"ProduceMessage", func() error {
			return svc.ProduceMessage(ctx, conn.ID, "t1", 0, []byte("k"), []byte("v"))
		}},
		{"MysqlUpdateCell", func() error {
			return svc.MysqlUpdateCell(ctx, conn.ID, "db", "t",
				model.MysqlCellValue{Column: "a", Value: strPtr("1")},
				[]model.MysqlCellValue{{Column: "id", Value: strPtr("1")}})
		}},
		{"CHUpdateCell", func() error {
			return svc.CHUpdateCell(ctx, conn.ID, "db", "t",
				model.CHCellValue{Column: "a", Type: "String", Value: strPtr("1")},
				[]model.CHCellValue{{Column: "id", Type: "Int64", Value: strPtr("1")}})
		}},
		{"RedisDeleteKeys", func() error {
			_, err := svc.RedisDeleteKeys(ctx, conn.ID, 0, []string{"k"})
			return err
		}},
	}
	for _, tc := range cases {
		err := tc.run()
		if err == nil {
			t.Fatalf("%s: expected rejection in readonly mode, got nil", tc.name)
		}
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: error %q does not contain %q", tc.name, err.Error(), want)
		}
		if !errors.Is(err, ErrReadonlyMode) {
			t.Fatalf("%s: error %q is not ErrReadonlyMode", tc.name, err.Error())
		}
	}

	// 只读方法不受影响:ListTopics 经 fake 工厂自动建连,照常返回 topic 列表。
	svc2, f := newTestService(t)
	f.k.topics = []*model.Topic{{Name: "orders"}}
	conn2, err := svc2.CreateConnection(ctx, sampleConn())
	if err != nil {
		t.Fatalf("create connection: %v", err)
	}
	enableReadonly(t, svc2, ctx)
	topics, err := svc2.ListTopics(ctx, conn2.ID)
	if err != nil {
		t.Fatalf("ListTopics in readonly mode: %v", err)
	}
	if len(topics) != 1 || topics[0].Name != "orders" {
		t.Fatalf("ListTopics = %+v, want one topic named orders", topics)
	}
}

// TestReadonlyModeToggleRestoresWrites 关闭开关后写方法恢复可用(fake 客户端
// 正常执行,不再被入口闸门拦截);闸门放行链路需要真实连接记录,先建一条。
func TestReadonlyModeToggleRestoresWrites(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	conn, err := svc.CreateConnection(ctx, sampleConn())
	if err != nil {
		t.Fatalf("create connection: %v", err)
	}

	if svc.IsReadonlyMode(ctx) {
		t.Fatal("readonly mode must default to off")
	}
	enableReadonly(t, svc, ctx)
	if err := svc.CreateTopic(ctx, conn.ID, "t1", 1, 1); !errors.Is(err, ErrReadonlyMode) {
		t.Fatalf("CreateTopic in readonly mode: err=%v, want ErrReadonlyMode", err)
	}

	if err := svc.SetReadonlyMode(ctx, false); err != nil {
		t.Fatalf("disable readonly mode: %v", err)
	}
	if svc.IsReadonlyMode(ctx) {
		t.Fatal("IsReadonlyMode = true after disabling")
	}
	// 恢复:闸门放行,请求落到 fake 客户端(返回 nil)。
	if err := svc.CreateTopic(ctx, conn.ID, "t1", 1, 1); err != nil {
		t.Fatalf("CreateTopic after disabling readonly mode: %v", err)
	}
}

// TestExecuteRejectsWriteStatementsInReadonlyMode 只读模式下 SQL 控制台
// Execute 拒绝非查询语句(逐源判定函数与各自控制台判型一致),查询语句
// 通过闸门(后续因无客户端报连接类错误,而非只读错误);ES DSL 控制台
// 仅放行 GET/HEAD。关闭开关后写语句照常通过闸门。
func TestExecuteRejectsWriteStatementsInReadonlyMode(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	enableReadonly(t, svc, ctx)

	const want = "生产安全模式已开启,写操作被拒绝"

	rejected := []struct {
		name string
		run  func() error
	}{
		{"MysqlExecute DELETE", func() error {
			_, err := svc.MysqlExecute(ctx, "c1", "db", "DELETE FROM t WHERE id = 1", 0, 0)
			return err
		}},
		{"MysqlExecute mixed script", func() error {
			// 混合脚本:任一语句为写即整体拒绝,放行集不被夹带穿透。
			_, err := svc.MysqlExecute(ctx, "c1", "db", "SELECT 1; DROP TABLE t", 0, 0)
			return err
		}},
		{"CHExecute ALTER DELETE", func() error {
			_, err := svc.CHExecute(ctx, "c1", "ALTER TABLE t DELETE WHERE id = 1", 0, 0)
			return err
		}},
		{"PostgresExecute UPDATE", func() error {
			_, err := svc.PostgresExecute(ctx, "c1", "db", "public", "UPDATE t SET a = 1", 0, 0)
			return err
		}},
		{"HiveExecute INSERT", func() error {
			_, err := svc.HiveExecute(ctx, "c1", "db", "INSERT INTO t VALUES (1)", 0, 0)
			return err
		}},
		{"EsExecute DROP", func() error {
			_, err := svc.EsExecute(ctx, "c1", "DROP TABLE t", 0, 0)
			return err
		}},
		{"EsDsl POST", func() error {
			_, err := svc.EsDsl(ctx, "c1", "POST", "/t/_delete_by_query", `{"query":{"match_all":{}}}`)
			return err
		}},
	}
	for _, tc := range rejected {
		err := tc.run()
		if !errors.Is(err, ErrReadonlyMode) {
			t.Fatalf("%s: err=%v, want ErrReadonlyMode", tc.name, err)
		}
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: error %q does not contain %q", tc.name, err.Error(), want)
		}
	}

	// 查询语句通过闸门:错误来自连接解析(连接不存在),而非只读拒绝。
	passed := []struct {
		name string
		run  func() error
	}{
		{"MysqlExecute SELECT", func() error {
			_, err := svc.MysqlExecute(ctx, "c1", "db", "SELECT 1", 0, 0)
			return err
		}},
		{"CHExecute SHOW", func() error {
			_, err := svc.CHExecute(ctx, "c1", "SHOW TABLES", 0, 0)
			return err
		}},
		{"HiveExecute EXPLAIN", func() error {
			_, err := svc.HiveExecute(ctx, "c1", "db", "EXPLAIN SELECT 1", 0, 0)
			return err
		}},
		{"EsDsl GET", func() error {
			_, err := svc.EsDsl(ctx, "c1", "GET", "/_cluster/health", "")
			return err
		}},
	}
	for _, tc := range passed {
		err := tc.run()
		if err == nil {
			t.Fatalf("%s: expected downstream error (no such connection), got nil", tc.name)
		}
		if errors.Is(err, ErrReadonlyMode) {
			t.Fatalf("%s: read statement must pass the readonly gate, got rejection", tc.name)
		}
	}

	// 关闭开关后写语句照常通过闸门(同样落到连接解析错误)。
	if err := svc.SetReadonlyMode(ctx, false); err != nil {
		t.Fatalf("disable readonly mode: %v", err)
	}
	if _, err := svc.MysqlExecute(ctx, "c1", "db", "DELETE FROM t WHERE id = 1", 0, 0); errors.Is(err, ErrReadonlyMode) {
		t.Fatal("write statement must pass the gate after readonly mode is off")
	}
}
