package main

import (
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/goburrow/modbus"
)

type AirMonitor struct {
	pm25      []float64
	avg       float32
	sum       float64
	waitlist  []float64
	offflag   bool
	waitflag  chan bool
	switchoff chan bool
	switchon  chan bool
}

func NewAirMonitor() *AirMonitor {
	return &AirMonitor{
		waitflag:  make(chan bool),
		switchoff: make(chan bool),
		switchon:  make(chan bool),
		avg:       15.0,
	}
}

func (am *AirMonitor) connectHandler(client mqtt.Client) {
	fmt.Println("Connected")
}

func (am *AirMonitor) connectLostHandler(client mqtt.Client, err error) {
	fmt.Printf("Connect lost: %v", err)
}

func (am *AirMonitor) sub(client mqtt.Client) {
	topic := "air/+"
	token := client.Subscribe(topic, 1, nil)
	token.Wait()
	fmt.Printf("Subscribed to topic %s", topic)
}

func (am *AirMonitor) readAir() {
	broker := os.Getenv("BROKER_IP")
	if broker == "" {
		fmt.Println("BROKER_IP environment variable is not set")
		return
	}
	var port = 1883
	opts := mqtt.NewClientOptions()
	opts.AddBroker(fmt.Sprintf("tcp://%s:%d", broker, port))
	opts.SetClientID("go_client")

	opts.SetDefaultPublishHandler(am.messagePubHandler)
	opts.OnConnect = am.connectHandler
	opts.OnConnectionLost = am.connectLostHandler
	mqttclient := mqtt.NewClient(opts)
	if token := mqttclient.Connect(); token.Wait() && token.Error() != nil {
		fmt.Println("Failed to connect to MQTT broker:", token.Error())
		return
	}
	am.sub(mqttclient)
}

func (am *AirMonitor) handleAirData() {
	modbusclient := am.initModbus()
	for {
		select {
		case <-am.switchoff:
			fmt.Println("Switch off now")
			am.turnOffAir(modbusclient)
			am.offflag = true
		case <-am.switchon:
			fmt.Println("Switch on now")
			am.turnOnAir(modbusclient)
			am.offflag = false
		}
	}
}

func (am *AirMonitor) turnOffAir(client modbus.Client) {
	results, err := client.WriteSingleRegister(1017, 0)
	if err != nil {
		fmt.Println(err)
	}
	fmt.Println(results)

	results, err = client.WriteSingleRegister(1018, 0)
	if err != nil {
		fmt.Println(err)
	}
	fmt.Println(results)
}

func (am *AirMonitor) turnOnAir(client modbus.Client) {
	results, err := client.WriteSingleRegister(1017, 2)
	if err != nil {
		fmt.Println(err)
	}
	fmt.Println(results)

	results, err = client.WriteSingleRegister(1018, 1)
	if err != nil {
		fmt.Println(err)
	}
	fmt.Println(results)
}

func (am *AirMonitor) initModbus() modbus.Client {
	handler := modbus.NewTCPClientHandler("192.168.178.33:502")
	handler.Timeout = 10 * time.Second
	handler.SlaveId = 0xFF
	err := handler.Connect()
	if err != nil {
		fmt.Println("Failed to connect to Modbus:", err)
		return nil
	}
	defer handler.Close()
	client := modbus.NewClient(handler)

	tag, err := client.ReadHoldingRegisters(1017, 1)
	if err != nil {
		fmt.Println(err)
	}

	nacht, err := client.ReadHoldingRegisters(1018, 1)
	if err != nil {
		fmt.Println(err)
	}
	fmt.Println(tag)
	fmt.Println(nacht)

	if tag[1] == 0 || nacht[1] == 0 {
		am.offflag = true
	} else {
		am.offflag = false
	}
	fmt.Println("offflag", am.offflag)

	return client
}

func (am *AirMonitor) messagePubHandler(client mqtt.Client, msg mqtt.Message) {
	am.sum = 0
	fmt.Printf("Received message: %s from topic: %s\n", msg.Payload(), msg.Topic())
	if msg.Topic() == "air/pm25" {
		fmt.Println("Topic is pm25")
		pmvalue, _ := strconv.ParseFloat(string(msg.Payload()), 64)

		if !am.offflag {
			if len(am.pm25) <= 200 {
				am.pm25 = append(am.pm25, pmvalue)
			} else {
				am.pm25 = am.pm25[1:]
				am.pm25 = append(am.pm25, pmvalue)
			}
			fmt.Println("Current Value List:", am.pm25)
		}

		for i := 0; i < len(am.pm25); i++ {
			am.sum += am.pm25[i]
		}
		if len(am.pm25) == 0 {
			fmt.Println("Len was 0")
			am.avg = float32(pmvalue)
		} else {
			am.avg = (float32(am.sum)) / (float32(len(am.pm25)))
		}

		fmt.Println("Average of pm25:", am.avg)

		if float32(pmvalue) >= am.avg+8 && !am.offflag {
			fmt.Println("Turn Air flow off")
			am.switchoff <- true
		} else if float32(pmvalue) < am.avg+6 && am.offflag {
			fmt.Println("Get ready to Turn Air flow on")
			am.waitlist = append(am.waitlist, pmvalue)
			fmt.Println("Waitlist is:", am.waitlist)
		} else if float32(pmvalue) >= am.avg+6 && am.offflag {
			am.waitlist = nil
			fmt.Println("Waitlist is nil", am.waitlist)
		}
		if len(am.waitlist) == 60 {
			fmt.Println("Turn Air flow on")
			am.switchon <- true
			am.waitlist = nil
		}
	}
}

func main() {
	am := NewAirMonitor()
	s := make(chan os.Signal)
	signal.Notify(s, os.Interrupt, syscall.SIGTERM)

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		am.readAir()
	}()

	go func() {
		defer wg.Done()
		am.handleAirData()
	}()

	<-s
	fmt.Println("Received termination signal, shutting down...")

	// Perform any necessary cleanup here

	wg.Wait()
	fmt.Println("All goroutines have finished, exiting.")
	os.Exit(0)
}
