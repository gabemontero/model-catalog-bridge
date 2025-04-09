# Model Catalog Schema

The goal of the [model catalog schema](./model-catalog.schema.json) is to provide a way to aggregate metadata for models and model servers into a consistent format that can be used to generate Backstage model catalog entities that represent them.

The schema can currently be used to map metadata to the Backstage catalog from the following scenarios:

1) A Model/inference server deployed with one or more models, and an exposed API
    - With `modelServer` and `modelServerAPI` defining the model server and API metadata
    - With `model` defining the model metadata
2) A standalone model, without a server or API exposing it
    - With only `models` specified using the schema

Freeform annotations can be provided on the model and modelServer objects in the form of key-value pairs.

<img width="706" alt="Screenshot 2025-03-17 at 4 10 34 PM" src="https://github.com/user-attachments/assets/6fb4d07c-ffe5-45b0-ae7d-9b8f42eb7e90" />

See below for how the metadata maps into Backstage catalog entities

## Model Catalog Structure:
In the Backstage Model Catalog: 
- Each model server is represented as a `Component` with type `model-server`, containing information such as:
   - Name, description URL, authentication status, and how to get access
- Each model deployed on a model server is represented as a `Resource` with type `ai-model`, containing information such as:
   - Name, description, model usage, intended tasks, tags, license, and author
- An `API` object representing the model server API type (of type `openai`, `grpc`, `graphql`, or `asyncapi`), which may include the API specification, and additional information about the model server's API.
- Each `model-server` Component `dependsOn`:
   - The 1 to N `ai-model` resources deployed on it
   - The `API` object associated with the model server

![AI Catalog](https://github.com/redhat-ai-dev/model-catalog-example/blob/main/assets/catalog-graph.png?raw=true "AI Catalog")

A reference model catalog schema can be found [here](https://github.com/redhat-ai-dev/model-catalog-example/blob/main/developer-model-service/catalog-info.yaml)

