// Based on:
// - https://github.com/looplab/logspout-logstash/blob/master/logstash.go
// - https://github.com/gettyimages/logspout-kafka/blob/master/kafka.go
// - https://github.com/gliderlabs/logspout/pull/41/files
// - https://github.com/fsouza/go-dockerclient/blob/master/container.go#L222

package redis

import (
	"encoding/json"
	"fmt"
	"github.com/garyburd/redigo/redis"
	"github.com/gliderlabs/logspout/router"
	"log"
	"os"
	"strings"
	"time"
)

type RedisAdapter struct {
	route       *router.Route
	pool        *redis.Pool
	key         string
	docker_host string
}

type DockerFields struct {
	Name       string `json:"name"`
	CID        string `json:"cid"`
	Image      string `json:"image"`
	ImageTag   string `json:"image_tag,omitempty"`
	Source     string `json:"source"`
	DockerHost string `json:"docker_host,omitempty"`
}

//MARATHON_APP_ID=/nene
//MARATHON_APP_DOCKER_IMAGE=docker.strava.com/strava/nene-server:0.2
//MARATHON_APP_VERSION=2015-11-18T23:31:02.730Z

type MarathonFields struct {
	Fake *string `json:"fake,omitempty"`
	Id   *string `json:"id,omitempty"`
}

type LogstashMessage struct {
	Timestamp      string                 `json:"@timestamp"`
	Sourcehost     string                 `json:"host"`
	Message        string                 `json:"message"`
	Data           map[string]interface{} `json:"data"`
	DockerFields   DockerFields           `json:"docker"`
	MarathonFields MarathonFields         `json:"marathon"`
}

func init() {
	router.AdapterFactories.Register(NewRedisAdapter, "redis")
}

func NewRedisAdapter(route *router.Route) (router.LogAdapter, error) {

	// add port if missing
	address := route.Address
	if !strings.Contains(address, ":") {
		address = address + ":6379"
	}

	key := route.Options["key"]
	if key == "" {
		key = getopt("REDIS_KEY", "logspout")
	}

	password := route.Options["password"]
	if password == "" {
		password = getopt("REDIS_PASSWORD", "")
	}

	docker_host := getopt("REDIS_DOCKER_HOST", "")

	if os.Getenv("DEBUG") != "" {
		log.Printf("Using Redis server '%s', password: %t, pushkey: '%s'\n",
			address, password != "", key)
	}

	pool := newRedisConnectionPool(address, password)

	// lets test the water
	conn := pool.Get()
	defer conn.Close()
	res, err := conn.Do("PING")
	if err != nil {
		return nil, errorf("Cannot connect to Redis server %s: %v", address, err)
	}
	if os.Getenv("DEBUG") != "" {
		log.Printf("Redis connect successful, got response: %s\n", res)
	}

	return &RedisAdapter{
		route:       route,
		pool:        pool,
		key:         key,
		docker_host: docker_host,
	}, nil
}

func (a *RedisAdapter) Stream(logstream chan *router.Message) {
	log.Println("start stream")
	conn := a.pool.Get()
	defer conn.Close()
	mute := false

	for m := range logstream {
		msg := createLogstashMessage(m, a.docker_host)
		js, err := json.Marshal(msg)
		if err != nil {
			if !mute {
				log.Println("redis: error on json.Marshal (muting until restored):", err)
				mute = true
			}
			continue
		}

		//log.Println("push", conn.Err())
		_, err = conn.Do("RPUSH", a.key, js)
		//log.Println("pushed")
		if err != nil {
			if !mute {
				log.Println("redis: error on rpush (muting until restored):", err)
				mute = true
			}
			continue
		}

		//log.Println("push", conn.Err())
		_, err = conn.Do("PUBLISH", "marathon_test", js)
		//log.Println("pushed")
		if err != nil {
			if !mute {
				log.Println("redis: error on rpush (muting until restored):", err)
				mute = true
			}
			continue
		}

		mute = false
		log.Println("ok", m)
	}
}

func errorf(format string, a ...interface{}) (err error) {
	err = fmt.Errorf(format, a...)
	if os.Getenv("DEBUG") != "" {
		fmt.Println(err.Error())
	}
	return
}

func getopt(name, dfault string) string {
	value := os.Getenv(name)
	if value == "" {
		value = dfault
	}
	return value
}

func newRedisConnectionPool(server, password string) *redis.Pool {
	return &redis.Pool{
		MaxIdle: 3,
		//MaxActive:   50,
		IdleTimeout: 240 * time.Second,
		Dial: func() (redis.Conn, error) {
			log.Println("redis dial!")
			c, err := redis.Dial("tcp", server)
			if err != nil {
				return nil, err
			}
			if password != "" {
				if _, err := c.Do("AUTH", password); err != nil {
					c.Close()
					return nil, err
				}
			}
			return c, err
		},
		TestOnBorrow: func(c redis.Conn, t time.Time) error {
			_, err := c.Do("PING")
			return err
		},
	}
}

func splitImage(image string) (string, string) {
	n := strings.Index(image, ":")
	if n > -1 {
		return image[0:n], image[n+1:]
	}
	return image, ""
}

func createLogstashMessage(m *router.Message, docker_host string) interface{} {
	image_name, image_tag := splitImage(m.Container.Config.Image)
	cid := m.Container.ID[0:12]
	name := m.Container.Name[1:]
	timestamp := m.Time.Format(time.RFC3339Nano)

	log.Println(m.Container.Config.Env)

	var x map[string]interface{}
	json.Unmarshal([]byte(m.Data), &x)

	log.Println(x)

	return LogstashMessage{
		Message:    m.Data,
		Data:       x,
		Timestamp:  timestamp,
		Sourcehost: m.Container.Config.Hostname,
		DockerFields: DockerFields{
			CID:        cid,
			Name:       name,
			Image:      image_name,
			ImageTag:   image_tag,
			Source:     m.Source,
			DockerHost: docker_host,
		},
		MarathonFields: MarathonFields{
			Id:   envValue("MARATHON_APP_ID", m.Container.Config.Env),
			Fake: envValue("FAKE", m.Container.Config.Env),
		},
	}
}

func envValue(a string, list []string) *string {
	// TODO doesn't work with escaped =, lots of problems...
	for _, b := range list {
		s := strings.Split(b, "=")
		if s[0] == a {
			return &s[len(s)-1]
		}
	}
	return nil
}
