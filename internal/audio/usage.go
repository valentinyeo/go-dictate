package audio

import "time"

type DailyUsage struct {
	day      string
	streamed time.Duration
	voiced   time.Duration
}

func (u *DailyUsage) Add(now time.Time, streamed, voiced time.Duration) (day string, streamedToday, voicedToday time.Duration) {
	day = now.Format("2006-01-02")
	if day != u.day {
		u.day = day
		u.streamed = 0
		u.voiced = 0
	}
	u.streamed += streamed
	u.voiced += voiced
	return day, u.streamed, u.voiced
}
