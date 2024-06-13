#!/bin/bash

for i in {1..30}
do
  go run main.go > "example-$i.txt"
done
