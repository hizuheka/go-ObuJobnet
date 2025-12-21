package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// decodeCalendar は Systemwalker の jobschprintr 形式（ビットマップ文字列）を解析し、
// 人間が読める形式の日付リスト文字列に変換して返します。
//
// 引数:
//   raw: "2025,04081020,..." 形式の文字列
//   fullDateMode: 日付を短縮せずに表示するかどうかのフラグ
// 戻り値:
//   "2025/01/01〜05; 2025/02/10" 形式の文字列
func decodeCalendar(raw string, fullDateMode bool) string {
	parts := strings.Split(raw, ",")
	
	// データ形式のバリデーション: 最低でも開始年と1ヶ月分のデータが必要です
	if len(parts) < 2 {
		return raw
	}

	// 1要素目は開始年（例: 2025）
	startYear, err := strconv.Atoi(parts[0])
	if err != nil {
		return raw // パース失敗時は生の文字列をそのまま返却
	}

	var activeDates []time.Time

	// 2要素目以降は、月ごとの稼働日ビットマップ（16進数8桁）が並んでいます
	// 最大で36ヶ月分（3年分）のデータが含まれます
	for i := 1; i < len(parts); i++ {
		hexStr := parts[i]
		if len(hexStr) != 8 {
			continue // データ長が不正な場合はスキップ
		}

		// 16進数文字列を32bit整数に変換
		val, err := strconv.ParseUint(hexStr, 16, 32)
		// パースエラー、または全てのビットが0（稼働日なし）の場合はスキップ
		if err != nil || val == 0 {
			continue
		}

		// 現在処理しているデータの年月を算出
		// i=1 が開始月となるため、オフセットは i-1 です
		monthOffset := i - 1
		year := startYear + (monthOffset / 12)          // 12ヶ月ごとに年を加算
		month := time.Month((monthOffset % 12) + 1)     // 1〜12月に正規化

		// ビットマップの解析
		// Systemwalker仕様: LSB(0ビット目)を1日、MSBに向かって日付が進む
		// 第0ビット=1日, 第1ビット=2日 ... 第30ビット=31日
		for day := 1; day <= 31; day++ {
			shift := day - 1
			mask := uint32(1 << shift)

			// 該当ビットが立っている(ON)か確認
			if (uint32(val) & mask) != 0 {
				// 日付オブジェクトを作成
				t := time.Date(year, month, day, 0, 0, 0, 0, time.Local)
				
				// 日付の正規化チェック
				// time.Dateは "2月30日" を "3月2日" 等に自動補正しますが、
				// ビットマップ上ではそれは不正データ（あり得ない日付のビットがON）であるため無視します
				if t.Month() != month {
					continue
				}
				activeDates = append(activeDates, t)
			}
		}
	}

	// 抽出した日付リストを、期間表示形式（〜）に圧縮して返却
	return formatDateRanges(activeDates, fullDateMode)
}

// formatDateRanges は日付のリストを受け取り、連続する日付を期間としてまとめます。
// 例: [1/1, 1/2, 1/3, 1/5] -> "1/1〜03; 1/5"
func formatDateRanges(dates []time.Time, fullDateMode bool) string {
	if len(dates) == 0 {
		return ""
	}

	var ranges []string
	start := dates[0]
	end := dates[0]

	for i := 1; i < len(dates); i++ {
		current := dates[i]
		
		// 連続性の判定: 現在の日付が、終了日の「翌日」と一致するか確認
		// AddDate(0, 0, 1)を使うことで、月跨ぎや年跨ぎ（1/31の次は2/1）も正しく判定可能
		if current.Equal(end.AddDate(0, 0, 1)) {
			// 連続している場合、期間の終了日を更新して次へ
			end = current
		} else {
			// 連続が途切れた場合、ここまでの期間を文字列化してリストに追加
			ranges = append(ranges, formatRangeString(start, end, fullDateMode))
			
			// 新しい期間の開始
			start = current
			end = current
		}
	}
	// 最後の期間を追加
	ranges = append(ranges, formatRangeString(start, end, fullDateMode))

	return strings.Join(ranges, "; ")
}

// formatRangeString は開始日と終了日の関係に基づき、最適な表示形式（短縮または完全）を生成します。
func formatRangeString(start, end time.Time, fullDateMode bool) string {
	const layoutFull = "2006/01/02" // 年/月/日
	const layoutMD = "01/02"       // 月/日
	const layoutD = "02"           // 日

	startStr := start.Format(layoutFull)

	// FullDateModeが有効、または期間ではなく単一日付の場合は、完全形式または単一表示
	if fullDateMode || start.Equal(end) {
		if start.Equal(end) {
			return startStr
		}
		return fmt.Sprintf("%s〜%s", startStr, end.Format(layoutFull))
	}

	// 以下、短縮表示ロジック
	
	// 同じ年の場合
	if start.Year() == end.Year() {
		// 同じ月の場合 -> 終了日は「日」のみ (例: 2025/01/01〜05)
		if start.Month() == end.Month() {
			return fmt.Sprintf("%s〜%s", startStr, end.Format(layoutD))
		}
		// 月が異なる場合 -> 終了日は「月/日」 (例: 2025/01/30〜02/02)
		return fmt.Sprintf("%s〜%s", startStr, end.Format(layoutMD))
	}

	// 年が異なる場合は省略不可 (例: 2025/12/31〜2026/01/03)
	return fmt.Sprintf("%s〜%s", startStr, end.Format(layoutFull))
}