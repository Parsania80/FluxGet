package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
)

type queueClient struct {
	commands chan string
	done     chan struct{}
	mu       sync.Mutex
	next     int
	pending  map[int]chan desktopResponse
}

func startQueue(executable string, maxActive int, speedLimit int64, update func(string), stopped func(error)) (*queueClient, error) {
	return startQueueWithSettings(executable, maxActive, speedLimit, "", update, stopped)
}

func startQueueWithSettings(executable string, maxActive int, speedLimit int64, settingsPath string, update func(string), stopped func(error)) (*queueClient, error) {
	return startConfiguredQueue(executable, maxActive, 4, speedLimit, settingsPath, update, stopped)
}

func startConfiguredQueue(executable string, maxActive, connections int, speedLimit int64, settingsPath string, update func(string), stopped func(error)) (*queueClient, error) {
	args := []string{"-queue", "-max-active", fmt.Sprint(maxActive), "-speed-limit", fmt.Sprint(speedLimit)}
	args = append(args, "-connections", fmt.Sprint(connections))
	if settingsPath != "" {
		args = append(args, "-live-speed-file", settingsPath)
	}
	cmd := exec.Command(executable, args...)
	input, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	reader, writer := io.Pipe()
	cmd.Stdout, cmd.Stderr = writer, writer
	if err := cmd.Start(); err != nil {
		input.Close()
		reader.Close()
		writer.Close()
		return nil, err
	}
	client := &queueClient{commands: make(chan string, 64), done: make(chan struct{}), pending: make(map[int]chan desktopResponse)}
	go func() {
		for {
			select {
			case command := <-client.commands:
				if _, err := fmt.Fprintln(input, command); err != nil {
					return
				}
			case <-client.done:
				return
			}
		}
	}()
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 4096), 2<<20)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "DESKTOP_JSON ") {
				var response desktopResponse
				if json.Unmarshal([]byte(strings.TrimPrefix(line, "DESKTOP_JSON ")), &response) == nil {
					client.mu.Lock()
					if target := client.pending[response.ID]; target != nil {
						target <- response
					}
					client.mu.Unlock()
				}
			} else {
				update(line)
			}
		}
		if err := scanner.Err(); err != nil {
			reader.CloseWithError(err)
		}
	}()
	go func() {
		err := cmd.Wait()
		writer.Close()
		<-readDone
		reader.Close()
		input.Close()
		close(client.done)
		stopped(err)
	}()
	return client, nil
}

type desktopRequest struct {
	ID          int            `json:"id"`
	Operation   string         `json:"operation"`
	Job         queueEntry     `json:"job"`
	MaxActive   int            `json:"max_active"`
	Connections int            `json:"connections"`
	Management  *requestPolicy `json:"management,omitempty"`
	Action      string         `json:"action,omitempty"`
	File        *replacement   `json:"file,omitempty"`
}

type desktopResponse struct {
	Revision   uint64          `json:"revision"`
	ID         int             `json:"id"`
	JobID      int             `json:"job_id"`
	Jobs       []queueEntry    `json:"jobs"`
	Error      string          `json:"error"`
	Management *budgetSnapshot `json:"management,omitempty"`
}

func (q *queueClient) request(ctx context.Context, request desktopRequest) (desktopResponse, error) {
	q.mu.Lock()
	q.next++
	request.ID = q.next
	target := make(chan desktopResponse, 1)
	q.pending[request.ID] = target
	q.mu.Unlock()
	defer func() { q.mu.Lock(); delete(q.pending, request.ID); q.mu.Unlock() }()
	data, err := json.Marshal(request)
	if err != nil {
		return desktopResponse{}, err
	}
	if err := q.send("desktop-json " + string(data)); err != nil {
		return desktopResponse{}, err
	}
	select {
	case response := <-target:
		if response.Error != "" {
			return response, errors.New(response.Error)
		}
		return response, nil
	case <-ctx.Done():
		return desktopResponse{}, ctx.Err()
	case <-q.done:
		return desktopResponse{}, errors.New("downloads service is unavailable; close any other FluxGet CLI queue and try again")
	}
}

func (q *queueClient) send(command string) error {
	if strings.ContainsAny(command, "\r\n") {
		return errors.New("invalid queue command")
	}
	select {
	case <-q.done:
		return errors.New("queue is disconnected")
	default:
	}
	select {
	case q.commands <- command:
		return nil
	case <-q.done:
		return errors.New("queue is disconnected")
	default:
		return errors.New("queue is busy")
	}
}

// Policy is sent explicitly to the scheduler; live speed polling never owns it.
type requestPolicy struct {
	Mode      string `json:"mode"`
	Total     int    `json:"total"`
	PerOrigin int    `json:"per_origin"`
}
type budgetSnapshot struct {
	Policy     requestPolicy `json:"policy"`
	Active     int           `json:"active"`
	Waiting    int           `json:"waiting"`
	FileTarget int           `json:"file_target,omitempty"`
	Draining   bool          `json:"draining,omitempty"`
	Status     string        `json:"status,omitempty"`
	Reason     string        `json:"reason,omitempty"`
}
type jobBudgetSnapshot struct {
	Active   int  `json:"active"`
	Waiting  int  `json:"waiting"`
	Cooldown bool `json:"cooldown,omitempty"`
}
type measurementSnapshot struct {
	Accepted    int64 `json:"accepted_bytes"`
	Requests    int64 `json:"requests"`
	LimiterWait int64 `json:"limiter_wait_ns"`
	BudgetWait  int64 `json:"budget_wait_ns"`
}
