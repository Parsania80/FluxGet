package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

type queueClient struct {
	commands chan string
	done     chan struct{}
}

func startQueue(executable string, maxActive int, speedLimit int64, update func(string), stopped func(error)) (*queueClient, error) {
	return startQueueWithSettings(executable, maxActive, speedLimit, "", update, stopped)
}

func startQueueWithSettings(executable string, maxActive int, speedLimit int64, settingsPath string, update func(string), stopped func(error)) (*queueClient, error) {
	args := []string{"-queue", "-max-active", fmt.Sprint(maxActive), "-speed-limit", fmt.Sprint(speedLimit)}
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
	client := &queueClient{commands: make(chan string, 64), done: make(chan struct{})}
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
			update(scanner.Text())
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
