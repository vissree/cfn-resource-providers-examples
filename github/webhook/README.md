# Vissree::GitHub::Webhook
This is a sample CloudFormation resource provider for managing Github Webhooks.

## Build
```
$ make clean && make build
```

## Local tests
Create a sample event json file in the below format.

```json
{
  "callbackContext": null,
  "action": "CREATE",
  "request": {
      "clientRequestToken": "91784c24-be70-4226-8158-2375d4f812ce",
      "desiredResourceState": {
            "Active": true,
            "ContentType": "json",
            "Events": [
                    "push"
                  ],
            "InsecureSSL": false,
            "Owner": "org-or-user-name",
            "PayloadURL": "https://sample.url",
            "Repo": "repo-name",
            "Secret": "",
            "Token": "xxxxxpersonalaccesstokenxxxx"
          },
      "previousResourceState": null,
      "logicalResourceIdentifier": null
    }
}
```

Run the local test using the event json file.
```
$ sam local invoke TestEntrypoint --event path/to/event.json
```

## Contract tests
Open a terminal and start a local lambda server
```
$ sam local start-lambda
```

Create an overrides file to specify the Repo, Owner, Token and Events properties. The random values generated from the patterns in the resource's property definitions will not work for the above properties. Name the file as `overrides.json` and save it at the root of the project.
```json
{
    "CREATE": {
        "/Repo": "repo-name",
        "/Owner": "org-or-user-name",
        "/Token": "xxxxxpersonalaccesstokenxxxx",
        "/Events": ["issues"]
    }
}
```

Build the model and run the contract tests.
```
$ make clean && make build
$ cfn test
```

Check the official ![documentation](https://github.com/aws-cloudformation/cloudformation-cli) for CloudFormation CLI for more options.

## Deploy
```
$ make build
$ cfn submit -v --region AWS_REGION
```

## Properties
| Name        | Type    | Description                                   | Required | Default  |
|-------------|---------|-----------------------------------------------|----------|----------|
| Repo        | String  | Repository name                               | Yes      |          |
| Owner       | String  | Organization/Username                         | Yes      |          |
| Token       | String  | Oauth/Personal access token                   | Yes      |          |
| PayloadURL  | String  | URL to which event payloads will be delivered | Yes      |          |
| Secret      | String  | The key to generate X-Hub-Signature header    | No       |          |
| InsecureSSL | Boolean | Enable/Disable SSL verification               | No       | false    |
| Events      | Array   | The events for which hook is triggered for    | No       | ["push"] |
| Active      | Boolean | Enable/Disable the hook                       | No       | true     |
