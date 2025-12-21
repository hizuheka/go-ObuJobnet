package main

// Config はアプリケーションの実行設定を保持する構造体です。
// コマンドライン引数から解析された値がここに格納されます。
type Config struct {
	// TargetDir: 処理対象のCSVファイルが格納されているルートディレクトリのパス
	TargetDir string
	
	// OutputFile: 結果を出力するファイルのパス (空文字の場合は標準出力を使用)
	OutputFile string
	
	// Debug: 詳細なデバッグログを出力するかどうかのフラグ
	Debug bool
	
	// FullDateMode: 日付範囲の短縮表示を行うかどうかのフラグ
	// trueの場合: "2025/01/01〜2025/01/05" (完全形式)
	// falseの場合: "2025/01/01〜05" (短縮形式・デフォルト)
	FullDateMode bool
}

// JobNet は抽出・解析された1つのジョブネット情報を保持する構造体です。
type JobNet struct {
	// SourceFile: このデータが抽出された元のファイル名（トレーサビリティ用）
	SourceFile string
	
	// Data: CSVの項目名(Header)をキー、その内容(Value)を値とするマップ
	// 項目名はファイルによって可変であるため、固定フィールドではなくMapを採用しています。
	Data map[string]string
}