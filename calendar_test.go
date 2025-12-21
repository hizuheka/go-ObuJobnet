package main

import (
	"testing"
	"time"
)

// TestDecodeCalendar はビットマップ文字列からの日付変換ロジックをテストします
func TestDecodeCalendar(t *testing.T) {
	tests := []struct {
		name         string
		raw          string
		fullDateMode bool
		want         string
	}{
		{
			name: "正常系: 1月1日のみ (LSB=1確認)",
			// 2025年, 1月=0x00000001 (1日目のみON), 他0
			raw:          "2025,00000001",
			fullDateMode: false,
			want:         "2025/01/01",
		},
		{
			name: "正常系: 1月連続 (1日〜5日)",
			// 1+2+4+8+16 = 31 (0x1F) -> 0000001F
			raw:          "2025,0000001F",
			fullDateMode: false,
			want:         "2025/01/01〜05",
		},
		{
			name: "正常系: 飛び石 (1日と3日)",
			// 1 + 4 = 5 (0x05) -> 00000005
			raw:          "2025,00000005",
			fullDateMode: false,
			want:         "2025/01/01; 2025/01/03",
		},
		{
			name: "正常系: 全日 (7FFFFFFF)",
			// 31ビット全てON
			raw:          "2025,7FFFFFFF",
			fullDateMode: false,
			want:         "2025/01/01〜31",
		},
		{
			name:         "オプション: FullDateMode有効",
			raw:          "2025,0000001F",
			fullDateMode: true,
			want:         "2025/01/01〜2025/01/05",
		},
		{
			name:         "異常系: 不正なフォーマット",
			raw:          "invalid_data",
			fullDateMode: false,
			want:         "invalid_data",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := decodeCalendar(tt.raw, tt.fullDateMode)
			if got != tt.want {
				t.Errorf("decodeCalendar() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestFormatDateRanges は日付リストから期間文字列への短縮ロジックをテストします
func TestFormatDateRanges(t *testing.T) {
	// ヘルパー: 日付生成
	d := func(y int, m time.Month, day int) time.Time {
		return time.Date(y, m, day, 0, 0, 0, 0, time.Local)
	}

	tests := []struct {
		name         string
		dates        []time.Time
		fullDateMode bool
		want         string
	}{
		{
			name:         "月内の短縮",
			dates:        []time.Time{d(2025, 1, 1), d(2025, 1, 2), d(2025, 1, 3)},
			fullDateMode: false,
			want:         "2025/01/01〜03",
		},
		{
			name:         "月跨ぎの短縮",
			dates:        []time.Time{d(2025, 1, 31), d(2025, 2, 1), d(2025, 2, 2)},
			fullDateMode: false,
			want:         "2025/01/31〜02/02",
		},
		{
			name:         "年跨ぎの短縮",
			dates:        []time.Time{d(2025, 12, 31), d(2026, 1, 1)},
			fullDateMode: false,
			want:         "2025/12/31〜2026/01/01",
		},
		{
			name:         "連続しない日付",
			dates:        []time.Time{d(2025, 1, 1), d(2025, 1, 5)},
			fullDateMode: false,
			want:         "2025/01/01; 2025/01/05",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatDateRanges(tt.dates, tt.fullDateMode)
			if got != tt.want {
				t.Errorf("formatDateRanges() = %v, want %v", got, tt.want)
			}
		})
	}
}
