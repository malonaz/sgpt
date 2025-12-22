#/bin/usr/bash

plz build //cmd/sgpt
sudo mv plz-out/bin/cmd/sgpt/sgpt /usr/local/bin/sgpt
sudo sh -c 'sgpt --local completion bash > /etc/bash_completion.d/sgpt'
