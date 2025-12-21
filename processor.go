package main

import (
	"bufio"
	"encoding/csv"
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

// Processor はジョブネット抽出処理の実行主体となる構造体です。
// 設定情報(Config)を保持し、それに従って処理を行います。
type Processor struct {
	Config *Config
}

// priorityHeaders: CSV出力時に優先して「左端（先頭）」に配置する列名
var priorityHeaders = []string{"jobnetname", "jobnetcomment"}

// tailHeaders: CSV出力時に優先して「右端（末尾）」に配置する列名
var tailHeaders = []string{"jobschprintr"}

// Run は処理のメインフローを実行します。
// 1. ディレクトリ探索・解析
// 2. 出力先の準備
// 3. CSV出力
func (p *Processor) Run() error {
	slog.Info("処理を開始します", "target_dir", p.Config.TargetDir)

	// 指定ディレクトリ内の全CSVファイルを解析
	jobNets, allHeaders, err := p.processDirectory()
	if err != nil {
		return fmt.Errorf("ディレクトリ処理エラー: %w", err)
	}

	// 出力先のWriterを取得（ファイル作成または標準出力）
	writer, closeFunc, err := p.getOutputWriter()
	if err != nil {
		return fmt.Errorf("出力先の準備エラー: %w", err)
	}
	defer closeFunc() // 関数終了時にファイルを閉じる

	// 結果をCSVとして書き出し
	if err := p.writeResult(writer, jobNets, allHeaders); err != nil {
		return fmt.Errorf("書き出しエラー: %w", err)
	}

	slog.Info("処理が完了しました", "total_records", len(jobNets))
	return nil
}

// processDirectory は指定されたディレクトリを再帰的に探索し、
// 対象となるCSVファイルからジョブネット情報を抽出します。
func (p *Processor) processDirectory() ([]JobNet, []string, error) {
	var jobNets []JobNet

	// headerSet: 全ファイルに出現する項目名を重複なく収集するためのセット
	// map[string]struct{} はメモリ効率の良いSetの実装パターンの1つ
	headerSet := make(map[string]struct{})

	// filepath.WalkDir を使用してディレクトリを効率的に探索
	err := filepath.WalkDir(p.Config.TargetDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// アクセス権限エラー等はログに出力して続行
			slog.Warn("ファイルアクセスエラー", "path", path, "error", err)
			return nil
		}

		// ディレクトリ自体や、拡張子が .csv でないファイルは無視
		if d.IsDir() || !strings.HasSuffix(strings.ToLower(d.Name()), ".csv") {
			return nil
		}

		// ファイル単位の解析処理を実行
		jn, headers, err := p.parseJobNetFile(path)
		if err != nil {
			slog.Warn("ファイルの解析をスキップしました", "path", path, "error", err)
			return nil
		}

		// 有効なデータが取得できた場合、リストに追加
		if jn != nil {
			jobNets = append(jobNets, *jn)
			// 出現したヘッダーをセットに登録
			for _, h := range headers {
				headerSet[h] = struct{}{}
			}
		}
		return nil
	})

	if err != nil {
		return nil, nil, err
	}

	// 集約したヘッダーを所定のルールでソートして返す
	return jobNets, p.sortHeaders(headerSet), nil
}

// parseJobNetFile は単一のCSVファイルを読み込み、NETブロックを抽出します。
func (p *Processor) parseJobNetFile(path string) (*JobNet, []string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()

	// Shift_JIS から UTF-8 への変換リーダーを作成
	// Systemwalkerのエクスポートデータは Shift_JIS であるため必須
	decoder := japanese.ShiftJIS.NewDecoder()
	scanner := bufio.NewScanner(transform.NewReader(f, decoder))

	// 行単位でスキャン
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// "NET" 行を検出したら、続く2行（ヘッダーとデータ）を解析対象とする
		if line == "NET" {
			// 次の行: 項目名(Header)
			if !scanner.Scan() {
				return nil, nil, fmt.Errorf("unexpected EOF (header missing)")
			}
			headerLine := scanner.Text()

			// さらに次の行: データ内容(Data)
			if !scanner.Scan() {
				return nil, nil, fmt.Errorf("unexpected EOF (data missing)")
			}
			dataLine := scanner.Text()

			// CSVとしてのパース処理へ委譲
			return p.parseCSVBlock(path, headerLine, dataLine)
		}
	}
	return nil, nil, scanner.Err()
}

// parseCSVBlock は抽出されたヘッダー行とデータ行をCSVとしてパースし、Map構造に変換します。
func (p *Processor) parseCSVBlock(filename, headerStr, dataStr string) (*JobNet, []string, error) {
	// ヘッダー行のパース
	hr := csv.NewReader(strings.NewReader(headerStr))
	headers, err := hr.Read()
	if err != nil {
		return nil, nil, err
	}

	// データ行のパース
	dr := csv.NewReader(strings.NewReader(dataStr))
	// FieldsPerRecord = -1 により、項目数とデータ数が不一致でもエラーにせず読み込む
	// (Systemwalkerの出力によっては空データなどで数が合わない場合があるため)
	dr.FieldsPerRecord = -1
	values, err := dr.Read()
	if err != nil {
		return nil, nil, err
	}

	// ヘッダーと値をMapに紐付け
	dataMap := make(map[string]string)
	for i, h := range headers {
		val := ""
		// 値が存在する場合のみ取得（インデックス範囲外アクセス防止）
		if i < len(values) {
			val = values[i]
		}

		// 特殊処理: jobschprintr列の場合はカレンダー変換ロジックを適用
		if h == "jobschprintr" {
			val = decodeCalendar(val, p.Config.FullDateMode)
		}

		dataMap[h] = val
	}
	return &JobNet{SourceFile: filename, Data: dataMap}, headers, nil
}

// sortHeaders は集約された全ての項目名を、以下の優先順位で並び替えます。
// 1. priorityHeaders (先頭固定)
// 2. その他の項目 (アルファベット昇順)
// 3. tailHeaders (末尾固定)
func (p *Processor) sortHeaders(headerSet map[string]struct{}) []string {
	var headList, tailList, otherList []string
	seen := make(map[string]bool)

	// 1. 先頭固定項目の抽出
	for _, h := range priorityHeaders {
		if _, exists := headerSet[h]; exists {
			headList = append(headList, h)
			seen[h] = true
		}
	}

	// 2. 末尾固定項目の抽出
	for _, h := range tailHeaders {
		if _, exists := headerSet[h]; exists {
			tailList = append(tailList, h)
			seen[h] = true
		}
	}

	// 3. その他の項目の抽出
	for h := range headerSet {
		if !seen[h] {
			otherList = append(otherList, h)
		}
	}
	// その他の項目は辞書順にソートして見つけやすくする
	sort.Strings(otherList)

	// 全リストを結合して返却
	return append(append(headList, otherList...), tailList...)
}

// getOutputWriter は出力設定に基づき、適切な io.Writer を返します。
// ファイル指定がある場合はファイルを作成し、指定がない場合は標準出力を返します。
// 呼び出し元で defer closeFunc() を実行する必要があります。
func (p *Processor) getOutputWriter() (io.Writer, func(), error) {
	if p.Config.OutputFile == "" {
		// 標準出力の場合は、閉じる必要がないため空の関数を返す
		return os.Stdout, func() {}, nil
	}

	f, err := os.Create(p.Config.OutputFile)
	if err != nil {
		return nil, nil, err
	}
	// ファイルの場合は Close メソッドをラップして返す
	return f, func() { f.Close() }, nil
}

// writeResult は整形されたデータをCSV形式で出力先に書き込みます。
func (p *Processor) writeResult(w io.Writer, jobNets []JobNet, headers []string) error {
	cw := csv.NewWriter(w)
	defer cw.Flush()

	// 1行目: ヘッダー出力
	if err := cw.Write(headers); err != nil {
		return err
	}

	// 2行目以降: データ出力
	for _, jn := range jobNets {
		record := make([]string, len(headers))
		for i, h := range headers {
			// Mapから該当項目の値を取得（存在しない場合は空文字）
			record[i] = jn.Data[h]
		}
		if err := cw.Write(record); err != nil {
			return err
		}
	}
	return nil
}
