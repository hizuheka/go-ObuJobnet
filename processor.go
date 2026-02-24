package main

import (
	"bufio"
	"context"
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

	"github.com/google/subcommands"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/transform"
)

// ConvertConfig は convert サブコマンドの実行設定を保持します。
// 将来他のコマンドが増えた際、設定が混ざらないよう名前を明確にしました。
type ConvertConfig struct {
	TargetDir    string
	OutputFile   string
	Debug        bool
	FullDateMode bool
}

// ConvertCmd は subcommands.Command インターフェースを実装し、
// convert サブコマンドのフラグ定義と実行を担当します。
type ConvertCmd struct {
	config ConvertConfig
}

// Name はコマンド名を返します。
func (*ConvertCmd) Name() string { return "convert" }

// Synopsis はコマンドの短い説明を返します。
func (*ConvertCmd) Synopsis() string {
	return "エクスポートデータからジョブネット情報を抽出しCSVに変換します"
}

// Usage はコマンドの使い方を返します。
func (*ConvertCmd) Usage() string {
	return `convert -dir <target_dir> [-out <output_file>] [-debug] [-full-date]:
  エクスポートデータからジョブネット情報を抽出しCSVに変換します。

Options:
`
}

// SetFlags はこのコマンド専用のフラグを定義します。
func (p *ConvertCmd) SetFlags(f *flag.FlagSet) {
	f.StringVar(&p.config.TargetDir, "dir", "", "対象のCSVファイルが含まれるフォルダパス (必須)")
	f.StringVar(&p.config.OutputFile, "out", "", "出力ファイルパス (省略時は標準出力)")
	f.BoolVar(&p.config.Debug, "debug", false, "デバッグログを出力する")
	f.BoolVar(&p.config.FullDateMode, "full-date", false, "日付の期間表示で年月の短縮を行わない")
}

// Execute はコマンドのメインロジックを実行します。
func (p *ConvertCmd) Execute(_ context.Context, f *flag.FlagSet, _ ...interface{}) subcommands.ExitStatus {
	// バリデーション
	if p.config.TargetDir == "" {
		fmt.Fprintln(os.Stderr, "エラー: 必須パラメータ -dir が指定されていません")
		f.Usage()
		return subcommands.ExitUsageError
	}

	// ロガーの設定 (main.goなどで定義されている共通関数を呼び出し)
	setupLogger(p.config.Debug)

	// メイン処理の実行
	proc := &Processor{Config: &p.config}
	if err := proc.Run(); err != nil {
		slog.Error("convert処理でエラーが発生しました", "error", err)
		return subcommands.ExitFailure
	}

	return subcommands.ExitSuccess
}

// Processor はジョブネット抽出処理の実行主体となる構造体です。
type Processor struct {
	Config *ConvertConfig
}

var priorityHeaders = []string{"foldername", "jobnetname", "jobnetcomment"}
var tailHeaders = []string{"jobschprintr"}

// Run は処理のメインフローを実行します。
// 1. ディレクトリ探索・解析
// 2. 出力先の準備
// 3. CSV出力
func (p *Processor) Run() error {
	slog.Info("convert処理を開始します", "target_dir", p.Config.TargetDir)

	jobNets, allHeaders, err := p.processDirectory()
	if err != nil {
		return fmt.Errorf("ディレクトリ処理エラー: %w", err)
	}

	writer, closeFunc, err := p.getOutputWriter()
	if err != nil {
		return fmt.Errorf("出力先の準備エラー: %w", err)
	}
	defer closeFunc()

	if err := p.writeResult(writer, jobNets, allHeaders); err != nil {
		return fmt.Errorf("書き出しエラー: %w", err)
	}

	slog.Info("convert処理が完了しました", "total_records", len(jobNets))
	return nil
}

func (p *Processor) processDirectory() ([]JobNet, []string, error) {
	var jobNets []JobNet
	headerSet := make(map[string]struct{})

	err := filepath.WalkDir(p.Config.TargetDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			slog.Warn("ファイルアクセスエラー", "path", path, "error", err)
			return nil
		}
		if d.IsDir() || !strings.HasSuffix(strings.ToLower(d.Name()), ".csv") {
			return nil
		}

		jn, headers, err := p.parseJobNetFile(path)
		if err != nil {
			slog.Warn("ファイルの解析をスキップしました", "path", path, "error", err)
			return nil
		}

		if jn != nil {
			jobNets = append(jobNets, *jn)
			for _, h := range headers {
				headerSet[h] = struct{}{}
			}
			headerSet["foldername"] = struct{}{}
		}
		return nil
	})

	if err != nil {
		return nil, nil, err
	}

	return jobNets, p.sortHeaders(headerSet), nil
}

func (p *Processor) parseJobNetFile(path string) (*JobNet, []string, error) {
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
				return nil, nil, fmt.Errorf("unexpected EOF (header missing)")
			}
			headerLine := scanner.Text()

			if !scanner.Scan() {
				return nil, nil, fmt.Errorf("unexpected EOF (data missing)")
			}
			dataLine := scanner.Text()

			return p.parseCSVBlock(path, headerLine, dataLine)
		}
	}
	return nil, nil, scanner.Err()
}

func (p *Processor) parseCSVBlock(path, headerStr, dataStr string) (*JobNet, []string, error) {
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

	dirPath := filepath.Dir(path)
	folderName := filepath.Base(dirPath)
	dataMap["foldername"] = folderName

	for i, h := range headers {
		val := ""
		if i < len(values) {
			val = values[i]
		}
		if h == "jobschprintr" {
			val = decodeCalendar(val, p.Config.FullDateMode)
		}
		dataMap[h] = val
	}

	return &JobNet{SourceFile: path, Data: dataMap}, headers, nil
}

func (p *Processor) sortHeaders(headerSet map[string]struct{}) []string {
	var headList, tailList, otherList []string
	seen := make(map[string]bool)

	for _, h := range priorityHeaders {
		if _, exists := headerSet[h]; exists {
			headList = append(headList, h)
			seen[h] = true
		}
	}

	for _, h := range tailHeaders {
		if _, exists := headerSet[h]; exists {
			tailList = append(tailList, h)
			seen[h] = true
		}
	}

	for h := range headerSet {
		if !seen[h] {
			otherList = append(otherList, h)
		}
	}
	sort.Strings(otherList)

	return append(append(headList, otherList...), tailList...)
}

func (p *Processor) getOutputWriter() (io.Writer, func(), error) {
	if p.Config.OutputFile == "" {
		return os.Stdout, func() {}, nil
	}

	f, err := os.Create(p.Config.OutputFile)
	if err != nil {
		return nil, nil, err
	}
	return f, func() { f.Close() }, nil
}

func (p *Processor) writeResult(w io.Writer, jobNets []JobNet, headers []string) error {
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
