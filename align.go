package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/google/subcommands"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/transform"
)

// AlignConfig は align サブコマンドの実行設定を保持します。
type AlignConfig struct {
	InputFile  string
	OutputFile string
}

// AlignCmd は subcommands.Command インターフェースを実装し、
// align サブコマンドの処理を担当します。
type AlignCmd struct {
	config AlignConfig
}

func (*AlignCmd) Name() string { return "align" }
func (*AlignCmd) Synopsis() string {
	return "CSVファイルの列数を揃え、項目を指定順に並び替えます"
}
func (*AlignCmd) Usage() string {
	return `align -in <input_csv> -out <output_csv>:
  エクスポートしたCSVファイルを読み込み、指定された項目の並び順に整形し、
  全ての行の列数が最大列数と一致するようにカンマを追加して出力します。
  ※ 元の括り文字（ダブルクォーテーション）は維持され、改行コードは CRLF になります。

Options:
`
}

func (p *AlignCmd) SetFlags(f *flag.FlagSet) {
	f.StringVar(&p.config.InputFile, "in", "", "入力CSVファイルパス (必須)")
	f.StringVar(&p.config.OutputFile, "out", "", "出力CSVファイルパス (必須)")
}

func (p *AlignCmd) Execute(_ context.Context, f *flag.FlagSet, _ ...interface{}) subcommands.ExitStatus {
	if p.config.InputFile == "" || p.config.OutputFile == "" {
		fmt.Fprintln(os.Stderr, "エラー: 必須パラメータが指定されていません")
		f.Usage()
		return subcommands.ExitUsageError
	}

	if err := p.run(); err != nil {
		slog.Error("align処理でエラーが発生しました", "error", err)
		return subcommands.ExitFailure
	}

	return subcommands.ExitSuccess
}

func (p *AlignCmd) run() error {
	// 1. 入力ファイルの読み込み
	inFile, err := os.Open(p.config.InputFile)
	if err != nil {
		return fmt.Errorf("入力ファイルのオープンエラー: %w", err)
	}
	defer inFile.Close()

	// Shift_JIS -> UTF-8 に変換しながら1行ずつ読み込む（Scannerを使用）
	decoder := japanese.ShiftJIS.NewDecoder()
	scanner := bufio.NewScanner(transform.NewReader(inFile, decoder))

	var records [][]string
	for scanner.Scan() {
		line := scanner.Text()
		line = strings.TrimRight(line, "\r\n")
		// カスタムパーサーで分割（元の括り文字を維持）
		records = append(records, splitCSVLine(line))
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("ファイルの読み込みエラー: %w", err)
	}

	// 指定項目をプログラム内にハードコーディング（要件に合わせて書き換えてください）
	targetNetCols := []string{"jobnetname", "jobnetcomment", "intervalstart", "msgonly", "holidayshift", "execattr", "messagename", "messagemode", "job", "operate", "noexecution"}
	targetJobCols := []string{"jobname", "jobnumber", "jobparam", "jobname_jes", "jobcomment", "directory", "normallimit", "limittime", "iconposition", "operate", "pre_job", "pre_job_endcode", "jobicon"}

	// パース用の状態遷移（ステートマシン）
	type State int
	const (
		StateNone State = iota
		StateNetHeader
		StateNetData
		StateJobHeader
		StateJobData
	)

	var outRecords [][]string
	state := StateNone
	var currentNetHeaders []string // アンクォートされたヘッダ名
	var currentJobHeaders []string // アンクォートされたヘッダ名

	// 2. 行単位の解析と並び替え
	for i, row := range records {
		if len(row) == 0 || isAllEmpty(row) {
			continue
		}

		// ブロックの開始タグを検知（アンクォートして厳密に判定）
		firstElem := strings.TrimSpace(unquote(row[0]))
		if firstElem == "NET" && len(row) == 1 {
			outRecords = append(outRecords, []string{"NET"})
			state = StateNetHeader
			continue
		}
		if firstElem == "JOB" && len(row) == 1 {
			outRecords = append(outRecords, []string{"JOB"})
			state = StateJobHeader
			continue
		}

		// 状態に応じた行の処理
		switch state {
		case StateNetHeader:
			currentNetHeaders = unquoteRow(row)
			if err := validateHeaders(currentNetHeaders, targetNetCols); err != nil {
				return fmt.Errorf("行 %d (NETヘッダ) のエラー: %w", i+1, err)
			}
			// ヘッダ行は alignHeaderRow を使って、不足項目名を補完する
			outRecords = append(outRecords, alignHeaderRow(row, currentNetHeaders, targetNetCols))
			state = StateNetData
		case StateNetData:
			outRecords = append(outRecords, alignRow(row, currentNetHeaders, targetNetCols))
		case StateJobHeader:
			currentJobHeaders = unquoteRow(row)
			if err := validateHeaders(currentJobHeaders, targetJobCols); err != nil {
				return fmt.Errorf("行 %d (JOBヘッダ) のエラー: %w", i+1, err)
			}
			// ヘッダ行は alignHeaderRow を使って、不足項目名を補完する
			outRecords = append(outRecords, alignHeaderRow(row, currentJobHeaders, targetJobCols))
			state = StateJobData
		case StateJobData:
			outRecords = append(outRecords, alignRow(row, currentJobHeaders, targetJobCols))
		default:
			// "NET"や"JOB"ブロック外の行はそのまま出力
			continue
		}
	}

	// 3. 最大列数の計算
	maxCols := 0
	for _, row := range outRecords {
		if len(row) > maxCols {
			maxCols = len(row)
		}
	}

	// 4. 不足している列数分、空文字（カンマ）を追加して長さを統一
	for i, row := range outRecords {
		if len(row) < maxCols {
			padded := make([]string, maxCols)
			copy(padded, row)
			outRecords[i] = padded
		}
	}

	// 5. 出力ファイルの作成
	outFile, err := os.Create(p.config.OutputFile)
	if err != nil {
		return fmt.Errorf("出力ファイルの作成エラー: %w", err)
	}
	defer outFile.Close()

	// UTF-8 -> Shift_JIS に変換するライターを作成
	encoder := japanese.ShiftJIS.NewEncoder()
	writer := transform.NewWriter(outFile, encoder)

	// CSVライブラリを使わず、自分で結合して書き出し（CRLF固定）
	for _, row := range outRecords {
		line := strings.Join(row, ",") + "\r\n"
		if _, err := io.WriteString(writer, line); err != nil {
			return fmt.Errorf("書き出しエラー: %w", err)
		}
	}

	slog.Info("CSVファイルの整形が完了しました", "output", p.config.OutputFile, "max_columns", maxCols)
	return nil
}

// isAllEmpty は行が完全に空、またはカンマのみで構成されているかを判定します。
func isAllEmpty(row []string) bool {
	for _, s := range row {
		if strings.TrimSpace(unquote(s)) != "" {
			return false
		}
	}
	return true
}

// splitCSVLine はCSVの1行を分割します。括り文字（"）は削除されずにそのまま保持されます。
func splitCSVLine(line string) []string {
	var fields []string
	var field strings.Builder
	inQuotes := false

	for i := 0; i < len(line); i++ {
		c := line[i]
		if c == '"' {
			inQuotes = !inQuotes
			field.WriteByte(c)
		} else if c == ',' && !inQuotes {
			fields = append(fields, field.String())
			field.Reset()
		} else {
			field.WriteByte(c)
		}
	}
	fields = append(fields, field.String())
	return fields
}

// unquote は文字列の両端にあるダブルクォーテーションを削除してトリムします（判定用）。
func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}

// unquoteRow は配列の全ての要素をアンクォートします。
func unquoteRow(row []string) []string {
	var res []string
	for _, s := range row {
		res = append(res, unquote(s))
	}
	return res
}

// validateHeaders は入力された項目(inCols)が、すべて指定された項目(targetCols)に含まれているか検証します。
// 含まれていない項目（未知の項目）があればエラーとします。
func validateHeaders(inCols, targetCols []string) error {
	targetMap := make(map[string]bool)
	for _, col := range targetCols {
		targetMap[col] = true
	}

	var unknown []string
	for _, col := range inCols {
		if !targetMap[col] {
			unknown = append(unknown, col)
		}
	}

	if len(unknown) > 0 {
		return fmt.Errorf("入力ファイルに指定外の項目が含まれています: %s", strings.Join(unknown, ", "))
	}
	return nil
}

// alignHeaderRow はヘッダ行を targetCols の並び順に合わせて再構築します。
// 入力ファイルに存在しない項目名の場合は、targetCols の項目名自体を出力します。
func alignHeaderRow(row []string, inCols []string, targetCols []string) []string {
	dataMap := make(map[string]string)
	for i, col := range inCols {
		if i < len(row) {
			dataMap[col] = row[i] // 元の文字列（括り文字維持）を保持
		}
	}

	newRow := make([]string, len(targetCols))
	for i, col := range targetCols {
		if val, ok := dataMap[col]; ok {
			newRow[i] = val // 入力ファイルに存在すれば、元の文字列をそのまま出力
		} else {
			newRow[i] = col // 入力ファイルに存在しなければ、項目名を出力
		}
	}
	return newRow
}

// alignRow はデータ行(row)を targetCols の並び順に合わせて再構築します。
// 入力ファイルに存在しない項目のデータは空文字になります。
func alignRow(row []string, inCols []string, targetCols []string) []string {
	dataMap := make(map[string]string)
	for i, col := range inCols {
		if i < len(row) {
			dataMap[col] = row[i] // 元の文字列（括り文字維持）を保持
		}
	}

	newRow := make([]string, len(targetCols))
	for i, col := range targetCols {
		// 存在しないキーの場合は、Goの仕様により空文字 "" が設定される
		newRow[i] = dataMap[col]
	}
	return newRow
}
