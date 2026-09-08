package job

import (
	"errors"
	"testing"
	"time"
)

func validJob() Job {
	return Job{
		ID:         "job-1",
		TenantID:   "t1",
		UserID:     "u1",
		PrinterID:  "p1",
		Dimensions: Dimensions{WidthMM: 50, HeightMM: 70},
		Copies:     1,
		Payload:    []byte{0x01, 0x02},
		CreatedAt:  time.Now(),
	}
}

func TestValidateAcceptsValidJob(t *testing.T) {
	if err := validJob().Validate(); err != nil {
		t.Fatalf("valid job rejected: %v", err)
	}
}

func TestValidateRejects(t *testing.T) {
	cases := map[string]func(*Job){
		"no id":         func(j *Job) { j.ID = "" },
		"no tenant":     func(j *Job) { j.TenantID = "" },
		"no user":       func(j *Job) { j.UserID = "" },
		"no printer":    func(j *Job) { j.PrinterID = "" },
		"zero copies":   func(j *Job) { j.Copies = 0 },
		"empty payload": func(j *Job) { j.Payload = nil },
		"bad dims":      func(j *Job) { j.Dimensions = Dimensions{} },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			j := validJob()
			mutate(&j)
			err := j.Validate()
			if err == nil {
				t.Fatal("expected validation error")
			}
			if !errors.Is(err, ErrInvalidJob) {
				t.Fatalf("error %v is not ErrInvalidJob", err)
			}
		})
	}
}
