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
	"strconv"
	"strings"
	"time"

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

// 優先して表示する列名のリスト
var priorityHeaders = []string{"jobnetname", "jobnetcomment"}

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

	slog.Info("処理が完了しました", "total_records", len(jobNets))
	return nil
}

// processDirectory はフォルダを探索し、ヘッダーのソート順を決定します
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

	// ヘッダーの並び替えロジック
	sortedHeaders := sortHeaders(headerSet)

	return jobNets, sortedHeaders, nil
}

// sortHeaders は指定されたルール（優先項目 -> アルファベット順）でソートします
func sortHeaders(headerSet map[string]struct{}) []string {
	var result []string
	seen := make(map[string]bool)

	// 1. 優先項目を追加
	for _, h := range priorityHeaders {
		if _, exists := headerSet[h]; exists {
			result = append(result, h)
			seen[h] = true
		}
	}

	// 2. その他の項目を収集
	var others []string
	for h := range headerSet {
		if !seen[h] {
			others = append(others, h)
		}
	}

	// 3. その他の項目をアルファベット順にソート
	sort.Strings(others)

	// 4. 結合
	result = append(result, others...)
	return result
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

		// 特殊処理: jobschprintr の場合は日付変換を行う
		if h == "jobschprintr" {
			val = decodeCalendar(val)
		}

		dataMap[h] = val
	}
	return &JobNet{SourceFile: filename, Data: dataMap}, headers, nil
}

// decodeCalendar はビットマップ文字列を日付リストに変換します
func decodeCalendar(raw string) string {
	parts := strings.Split(raw, ",")
	// 最低でも "YYYY, Hex..." の形式が必要
	if len(parts) < 2 {
		return raw
	}

	startYear, err := strconv.Atoi(parts[0])
	if err != nil {
		return raw // 年がパースできない場合は変換しない
	}

	var formattedDates []string

	// 2要素目以降が月ごとのビットマップ (最大36ヶ月)
	for i := 1; i < len(parts); i++ {
		hexStr := parts[i]
		if len(hexStr) != 8 {
			continue // 8桁でない場合は無視
		}

		// 16進数をパース (32bit)
		val, err := strconv.ParseUint(hexStr, 16, 32)
		if err != nil {
			continue
		}

		// 全ビットが0ならその月は稼働なしなのでスキップ（高速化）
		if val == 0 {
			continue
		}

		// 年月の計算 (i=1が開始月)
		monthOffset := i - 1
		year := startYear + (monthOffset / 12)
		month := time.Month((monthOffset % 12) + 1)

		// ビットチェック (1日〜31日)
		// Systemwalker仕様: 左端(MSB) = 1日
		// 0x80000000 (Binary: 1000...) -> 1日
		for day := 1; day <= 31; day++ {
			// day-1 だけ左シフトしたビットマスクを作成し、MSB側からチェックするための計算
			// MSB(31bit目)を1日目とするため、シフト量は (31 - (day-1)) となる
			shift := 31 - (day - 1)
			mask := uint32(1 << shift)

			if (uint32(val) & mask) != 0 {
				formattedDates = append(formattedDates, fmt.Sprintf("%04d/%02d/%02d", year, month, day))
			}
		}
	}

	// セミコロン区切りで結合して返す
	return strings.Join(formattedDates, "; ")
}

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

func setupLogger(debug bool) {
	level := slog.LevelInfo
	if debug {
		level = slog.LevelDebug
	}
	opts := &slog.HandlerOptions{Level: level}
	logger := slog.New(slog.NewTextHandler(os.Stderr, opts))
	slog.SetDefault(logger)
}

func getOutputWriter(path string) (io.Writer, func(), error) {
	if path == "" {
		return os.Stdout, func() {}, nil
	}
	f, err := os.Create(path)
	if err != nil {
		return nil, nil, err
	}
	return f, func() { f.Close() }, nil
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
