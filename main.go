package main

import (
	"bufio"
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/transform"
)

// Config はアプリケーションの実行設定を保持します
type Config struct {
	TargetDir  string
	OutputFile string
	Debug      bool
}

// JobNet は抽出したジョブネットの情報を保持します
type JobNet struct {
	SourceFile string
	Data       map[string]string
}

// main はエントリーポイントです。依存性の注入や終了コードの制御のみを行います。
func main() {
	// 1. 設定の読み込み
	cfg, err := parseFlags()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		flag.Usage()
		os.Exit(1)
	}

	// 2. ロガーのセットアップ
	setupLogger(cfg.Debug)

	// 3. アプリケーションの実行
	if err := run(cfg); err != nil {
		slog.Error("プログラムが失敗しました", "error", err)
		os.Exit(1)
	}
}

// run はアプリケーションの主要なロジックを実行します。
// mainから分離することで、テストが容易になり、可読性が向上します。
func run(cfg *Config) error {
	slog.Info("処理を開始します", "target_dir", cfg.TargetDir)

	// CSV解析処理
	jobNets, allHeaders, err := processDirectory(cfg.TargetDir)
	if err != nil {
		return fmt.Errorf("ディレクトリ処理エラー: %w", err)
	}

	// 出力先の準備（Writerの取得とクローズ処理の取得）
	writer, closeFunc, err := getOutputWriter(cfg.OutputFile)
	if err != nil {
		return fmt.Errorf("出力先の準備エラー: %w", err)
	}
	defer closeFunc()

	// 書き出し
	if err := writeResult(writer, jobNets, allHeaders); err != nil {
		return fmt.Errorf("書き出しエラー: %w", err)
	}

	slog.Info("処理が完了しました", "total", len(jobNets))
	return nil
}

// parseFlags はコマンドライン引数を解析してConfigを返します
func parseFlags() (*Config, error) {
	cfg := &Config{}
	flag.StringVar(&cfg.TargetDir, "dir", "", "対象のCSVファイルが含まれるフォルダパス (必須)")
	flag.StringVar(&cfg.OutputFile, "out", "", "出力ファイルパス (省略時は標準出力)")
	flag.BoolVar(&cfg.Debug, "debug", false, "デバッグログを出力する")
	flag.Parse()

	if cfg.TargetDir == "" {
		return nil, fmt.Errorf("エラー: 必須パラメータ -dir が指定されていません")
	}
	return cfg, nil
}

// setupLogger はslogの設定を行います
func setupLogger(debug bool) {
	level := slog.LevelInfo
	if debug {
		level = slog.LevelDebug
	}
	opts := &slog.HandlerOptions{Level: level}
	// ログは常にStderrに出し、データ出力(Stdout)と分離する
	logger := slog.New(slog.NewTextHandler(os.Stderr, opts))
	slog.SetDefault(logger)
}

// getOutputWriter は出力先に応じたWriterと、それを閉じるための関数を返します
func getOutputWriter(path string) (io.Writer, func(), error) {
	if path == "" {
		// 標準出力の場合は何もしないClose関数を返す
		return os.Stdout, func() {}, nil
	}

	f, err := os.Create(path)
	if err != nil {
		return nil, nil, err
	}
	// ファイルの場合はCloseを実行する関数を返す
	return f, func() { f.Close() }, nil
}

func processDirectory(root string) ([]JobNet, []string, error) {
	var jobNets []JobNet
	headerSet := make(map[string]struct{})

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			slog.Warn("ファイルアクセスエラー", "path", path, "error", err)
			return nil
		}
		if d.IsDir() || !strings.HasSuffix(strings.ToLower(d.Name()), ".csv") {
			return nil
		}

		slog.Debug("解析中", "path", path)
		jn, headers, err := parseJobNetFile(path)
		if err != nil {
			slog.Warn("スキップ", "path", path, "error", err)
			return nil
		}

		if jn != nil {
			jobNets = append(jobNets, *jn)
			for _, h := range headers {
				headerSet[h] = struct{}{}
			}
		}
		return nil
	})

	if err != nil {
		return nil, nil, err
	}

	allHeaders := make([]string, 0, len(headerSet))
	for h := range headerSet {
		allHeaders = append(allHeaders, h)
	}
	sort.Strings(allHeaders)

	return jobNets, allHeaders, nil
}

func parseJobNetFile(path string) (*JobNet, []string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()

	decoder := japanese.ShiftJIS.NewDecoder()
	scanner := bufio.NewScanner(transform.NewReader(f, decoder))

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "NET" {
			if !scanner.Scan() {
				return nil, nil, fmt.Errorf("unexpected EOF (header)")
			}
			headerLine := scanner.Text()
			if !scanner.Scan() {
				return nil, nil, fmt.Errorf("unexpected EOF (data)")
			}
			dataLine := scanner.Text()
			return parseCSVBlock(path, headerLine, dataLine)
		}
	}
	return nil, nil, scanner.Err()
}

func parseCSVBlock(filename, headerStr, dataStr string) (*JobNet, []string, error) {
	hr := csv.NewReader(strings.NewReader(headerStr))
	headers, err := hr.Read()
	if err != nil {
		return nil, nil, err
	}

	dr := csv.NewReader(strings.NewReader(dataStr))
	dr.FieldsPerRecord = -1
	values, err := dr.Read()
	if err != nil {
		return nil, nil, err
	}

	dataMap := make(map[string]string)
	for i, h := range headers {
		val := ""
		if i < len(values) {
			val = values[i]
		}
		dataMap[h] = val
	}
	return &JobNet{SourceFile: filename, Data: dataMap}, headers, nil
}

func writeResult(w io.Writer, jobNets []JobNet, headers []string) error {
	cw := csv.NewWriter(w)
	defer cw.Flush()

	if err := cw.Write(headers); err != nil {
		return err
	}
	for _, jn := range jobNets {
		record := make([]string, len(headers))
		for i, h := range headers {
			record[i] = jn.Data[h]
		}
		if err := cw.Write(record); err != nil {
			return err
		}
	}
	return nil
}
