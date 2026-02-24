package main

import (
	"context"
	"flag"
	"log/slog"
	"os"

	"github.com/google/subcommands"
)

// main はアプリケーションのエントリーポイントです。
func main() {
	// 1. subcommands に組み込みの便利なコマンド群を登録
	subcommands.Register(subcommands.HelpCommand(), "")
	subcommands.Register(subcommands.FlagsCommand(), "")
	subcommands.Register(subcommands.CommandsCommand(), "")

	// 2. アプリケーション固有のサブコマンドを登録
	subcommands.Register(&ConvertCmd{}, "")
	subcommands.Register(&AlignCmd{}, "")

	// 3. フラグのパースとコマンドの実行
	flag.Parse()
	ctx := context.Background()
	os.Exit(int(subcommands.Execute(ctx)))
}

// setupLogger はログ出力の設定を行います。
// ログは常に標準エラー出力(Stderr)に向け、CSVデータ(Stdout)と混ざらないようにします。
func setupLogger(debug bool) {
	level := slog.LevelInfo
	if debug {
		level = slog.LevelDebug
	}
	opts := &slog.HandlerOptions{Level: level}
	logger := slog.New(slog.NewTextHandler(os.Stderr, opts))
	slog.SetDefault(logger)
}
