/*
Copyright 2018 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package subscriber

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"cloud.google.com/go/pubsub"
	"github.com/sirupsen/logrus"
	"k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/kube"
	"k8s.io/test-infra/prow/pubsub/reporter"
)

type kubeTestClient struct {
	pj *kube.ProwJob
}

func (c *kubeTestClient) CreateProwJob(job kube.ProwJob) (kube.ProwJob, error) {
	c.pj = &job
	return job, nil
}

func TestHandleMessage(t *testing.T) {
	for _, tc := range []struct {
		name   string
		msg    *pubsub.Message
		s      string
		config *config.Config
		err    string
		labels []string
	}{
		{
			name: "PeriodicJobNoPubsub",
			msg: &pubsub.Message{
				ID: "fakeID",
				Attributes: map[string]string{
					prowEventType: periodicProwJob,
					prowJobName:   "test",
				},
			},
			config: &config.Config{
				JobConfig: config.JobConfig{
					Periodics: []config.Periodic{
						{
							JobBase: config.JobBase{
								Name: "test",
							},
						},
					},
				},
			},
		},
		{
			name: "UnknownEventType",
			msg: &pubsub.Message{
				ID: "fakeID",
				Attributes: map[string]string{
					prowEventType: "UnknownEventType",
				},
			},
			config: &config.Config{},
			err:    "unsupported event type",
			labels: []string{reporter.PubsubTopicLabel, reporter.PubsubRunIDLabel, reporter.PubsubProjectLabel},
		},
	} {
		t.Run(tc.name, func(t1 *testing.T) {
			kc := &kubeTestClient{}
			ca := &config.Agent{}
			ca.Set(tc.config)
			s := Subscriber{
				Metrics:     NewMetrics(),
				kc:          kc,
				logEntry:    logrus.NewEntry(logrus.New()),
				ConfigAgent: ca,
			}
			if err := s.handleMessage(tc.msg, tc.s); err != nil {
				if err.Error() != tc.err {
					t1.Errorf("Expected error %v got %v", tc.err, err.Error())
				} else if tc.err == "" {
					if kc.pj == nil {
						t.Errorf("Prow job not created")
					}
					for _, k := range tc.labels {
						if _, ok := kc.pj.Labels[k]; !ok {
							t.Errorf("label %s is missing", k)
						}
					}
				}
			}
		})
	}
}

func TestHandlePeriodicJob(t *testing.T) {
	for _, tc := range []struct {
		name   string
		msg    *pubsub.Message
		s      string
		config *config.Config
		err    string
	}{
		{
			name: "PeriodicJobNoPubsub",
			msg: &pubsub.Message{
				ID: "fakeID",
				Attributes: map[string]string{
					prowEventType: periodicProwJob,
					prowJobName:   "test",
				},
			},
			config: &config.Config{
				JobConfig: config.JobConfig{
					Periodics: []config.Periodic{
						{
							JobBase: config.JobBase{
								Name: "test",
							},
						},
					},
				},
			},
		},
		{
			name: "PeriodicJobPubsubSet",
			msg: &pubsub.Message{
				ID: "fakeID",
				Attributes: map[string]string{
					prowEventType:               periodicProwJob,
					prowJobName:                 "test",
					reporter.PubsubProjectLabel: "project",
					reporter.PubsubRunIDLabel:   "runid",
					reporter.PubsubTopicLabel:   "topic",
				},
			},
			config: &config.Config{
				JobConfig: config.JobConfig{
					Periodics: []config.Periodic{
						{
							JobBase: config.JobBase{
								Name: "test",
							},
						},
					},
				},
			},
		},
		{
			name: "JobNotFound",
			msg: &pubsub.Message{
				ID: "fakeID",
				Attributes: map[string]string{
					prowEventType: periodicProwJob,
					prowJobName:   "test",
				},
			},
			config: &config.Config{},
			err:    "failed to find associated periodic job",
		},
	} {
		t.Run(tc.name, func(t1 *testing.T) {
			kc := &kubeTestClient{}
			ca := &config.Agent{}
			ca.Set(tc.config)
			s := Subscriber{
				Metrics:     NewMetrics(),
				kc:          kc,
				logEntry:    logrus.NewEntry(logrus.New()),
				ConfigAgent: ca,
			}
			if err := s.handlePeriodicJob(logrus.NewEntry(logrus.New()), tc.msg, tc.s); err != nil {
				if err.Error() != tc.err {
					t1.Errorf("Expected error %v got %v", tc.err, err.Error())
				} else if tc.err == "" {
					if kc.pj == nil {
						t.Errorf("Prow job not created")
					}
				}
			}
		})
	}
}

func TestPushServer_ServeHTTP(t *testing.T) {
	kc := &kubeTestClient{}
	pushServer := PushServer{
		Subscriber: Subscriber{
			ConfigAgent: &config.Agent{},
			Metrics:     NewMetrics(),
			logEntry:    logrus.NewEntry(logrus.New()),
			kc:          kc,
		},
	}
	for _, tc := range []struct {
		name         string
		url          string
		secret       string
		pushRequest  interface{}
		expectedCode int
	}{
		{
			name:   "WrongToken",
			secret: "wrongToken",
			url:    "https://prow.k8s.io/push",
			pushRequest: pushRequest{
				Message: message{
					ID: "runid",
				},
			},
			expectedCode: http.StatusForbidden,
		},
		{
			name:   "NoToken",
			url:    "https://prow.k8s.io/push",
			pushRequest: pushRequest{
				Message: message{
					ID: "runid",
				},
			},
			expectedCode: http.StatusNotModified,
		},
		{
			name:   "RightToken",
			secret: "secret",
			url:    "https://prow.k8s.io/push?token=secret",
			pushRequest: pushRequest{
				Message: message{
					ID: "runid",
				},
			},
			expectedCode: http.StatusNotModified,
		},
		{
			name:         "InvalidPushRequest",
			secret:       "secret",
			url:          "https://prow.k8s.io/push?token=secret",
			pushRequest:  "invalid",
			expectedCode: http.StatusBadRequest,
		},
		{
			name:   "SuccessToken",
			secret: "secret",
			url:    "https://prow.k8s.io/push?token=secret",
			pushRequest: pushRequest{
				Message: message{
					ID: "fakeID",
					Attributes: map[string]string{
						prowEventType: periodicProwJob,
						prowJobName:   "test",
					},
				},
			},
			expectedCode: http.StatusOK,
		},
		{
			name:   "SuccessNoToken",
			url:    "https://prow.k8s.io/push",
			pushRequest: pushRequest{
				Message: message{
					ID: "fakeID",
					Attributes: map[string]string{
						prowEventType: periodicProwJob,
						prowJobName:   "test",
					},
				},
			},
			expectedCode: http.StatusOK,
		},
	} {
		t.Run(tc.name, func(t1 *testing.T) {
			config := &config.Config{
				JobConfig: config.JobConfig{
					Periodics: []config.Periodic{
						{
							JobBase: config.JobBase{
								Name: "test",
							},
						},
					},
				},
			}
			pushServer.Subscriber.ConfigAgent.Set(config)
			pushServer.PushSecret = tc.secret
			kc.pj = nil

			body := new(bytes.Buffer)

			if err := json.NewEncoder(body).Encode(tc.pushRequest); err != nil {
				t1.Errorf(err.Error())
			}
			req := httptest.NewRequest(http.MethodPost, tc.url, body)
			w := httptest.NewRecorder()
			pushServer.ServeHTTP(w, req)
			resp := w.Result()
			if resp.StatusCode != tc.expectedCode {
				t1.Errorf("exected code %d got %d", tc.expectedCode, resp.StatusCode)
			}
		})
	}
}
