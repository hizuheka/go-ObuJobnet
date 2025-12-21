package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
)

// main はアプリケーションのエントリーポイントです。
func main() {
	// 1. コマンドライン引数の解析
	cfg, err := parseFlags()
	if err != nil {
		// 引数エラー時は標準エラー出力にメッセージを出して終了
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	// 2. ロガーの初期化
	setupLogger(cfg.Debug)

	// 3. Processorのインスタンス化と実行
	// 同じパッケージ(main)に属するため、Processor構造体を直接利用可能
	proc := &Processor{Config: cfg}
	if err := proc.Run(); err != nil {
		slog.Error("プログラムの実行中にエラーが発生しました", "error", err)
		os.Exit(1)
	}
}

// parseFlags はコマンドライン引数を解析し、Config構造体を生成します。
func parseFlags() (*Config, error) {
	cfg := &Config{}

	// 引数定義
	flag.StringVar(&cfg.TargetDir, "dir", "", "対象のCSVファイルが含まれるフォルダパス (必須)")
	flag.StringVar(&cfg.OutputFile, "out", "", "出力ファイルパス (省略時は標準出力)")
	flag.BoolVar(&cfg.Debug, "debug", false, "デバッグログを出力する")
	flag.BoolVar(&cfg.FullDateMode, "full-date", false, "日付の期間表示で年月の短縮を行わない (例: 2025/01/01〜2025/01/05)")

	flag.Parse()

	// 必須パラメータのチェック
	if cfg.TargetDir == "" {
		return nil, fmt.Errorf("エラー: 必須パラメータ -dir が指定されていません")
	}
	return cfg, nil
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
