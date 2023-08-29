package diff

const prompt = `IMPORTANT: Provide only plain text without Markdown formatting.
IMPORTANT: Do not include markdown formatting such as "@@@".
Output a git commit messages for the provided diff using the following format:
@@@
[{scope}] - {type}: {summary}

 - {bullet_point}
 - {bullet_point}
@@@

Documentation:
@@@
 summary: A 50 character summary. should be present tense. Not capitalized. No period in the end.”, and imperative like the type.
 scope: The package or module that is affected by the change. This field is optional, only include it if the changes particularly target a single area.
        If the no particular area can be targeted, use "misc". If most of the changes happen in ./folder_a/, then the scope would be @folder_a@
 type: One of "fix, feature, refactor, test, devops, docs". Indicates the type of change being done.
 bullet_point: An sentence explaining why we’re changing the code, compared to what it was before.
@@@

Examples:
@@@
[reporting] - feature: add automatic generation of PnL reports for competitors

 - Every 24 hours, a job is triggered to generate the PnL reports of all competitors and upload them to an S3 bucket
 - Failed jobs are retried with an exponential backoff
@@@
@@@
[trading] - refactor: remove @gas_limit@ field from @Calldata@ protobuf

 - @gas_limit@ has been replaced by @gas_price@ and all clients have stopped using it
@@@
@@@
[price_model] - test: cover case where Binance price feed disconnects
@@@
@@@
[env] - devops: add ClusterRoleBinding between price-model ServiceAccount and grpc-client-kube-resolver ClusterRole
@@@

`

const generateGitCommitMessage = `Generate a git commit message.
Think step-by-step to ensure you only write about meaningful high-level changes.
Try to understand what the diff aims to do rather than focus on the details.
{{message}}
`
