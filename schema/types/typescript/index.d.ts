/**
 * Schema for defining AI models and model servers, for conversion to Backstage catalog
 * entities
 */
export interface ModelCatalog {
    /**
     * An array of AI models to be imported into the Backstage catalog
     */
    models: Model[];
    /**
     * A deployed model server running one or more models, exposed over an API
     */
    modelServer?: ModelServer;
}

/**
 * A deployed model server running one or more models, exposed over an API
 *
 * Schema for defining AI model servers, for conversion to Backstage catalog entities
 */
export interface ModelServer {
    /**
     * Annotations relating to the model server, in key-value pair format
     */
    annotations?: { [key: string]: string };
    /**
     * The API metadata associated with the model server
     */
    API?: API;
    /**
     * Whether or not the model server requires authentication to access
     */
    authentication?: boolean;
    /**
     * A description of the model server and what it's for
     */
    description: string;
    /**
     * The URL for the model server's homepage, if present
     */
    homepageURL?: string;
    /**
     * The lifecycle state of the model server API
     */
    lifecycle: string;
    /**
     * The name of the model server
     */
    name: string;
    /**
     * The Backstage user that will be responsible for the model server
     */
    owner: string;
    /**
     * Descriptive tags for the model server
     */
    tags?: string[];
    /**
     * How to use and interact with the model server
     */
    usage?: string;
}

/**
 * The API metadata associated with the model server
 *
 * Schema for defining the API exposed by model servers, for conversion to Backstage catalog
 * entities
 */
export interface API {
    /**
     * A link to the schema used by the model server API
     */
    spec: string;
    /**
     * Descriptive tags for the model server's API
     */
    tags?: string[];
    /**
     * The type of API that the model server exposes
     */
    type: Type;
    /**
     * The URL that the model server's REST API is exposed over, how the model(s) are interacted
     * with
     */
    url: string;
}

/**
 * The type of API that the model server exposes
 */
export enum Type {
    Asyncapi = "asyncapi",
    Graphql = "graphql",
    Grpc = "grpc",
    Openapi = "openapi",
}

/**
 * An AI model to be imported into the Backstage catalog
 *
 * Schema for defining AI models conversion to Backstage catalog entities
 */
export interface Model {
    /**
     * Annotations relating to the model, in key-value pair format
     */
    annotations?: { [key: string]: string };
    /**
     * A URL to access the model's artifacts, e.g. on HuggingFace, Minio, Github, etc
     */
    artifactLocationURL?: string;
    /**
     * A description of the model and what it's for
     */
    description: string;
    /**
     * Any ethical considerations for the model
     */
    ethics?: string;
    /**
     * The URL pointing to any specific documentation on how to use the model on the model server
     */
    howToUseURL?: string;
    /**
     * The lifecycle state of the model server API
     */
    lifecycle: string;
    /**
     * The name of the model
     */
    name: string;
    /**
     * The Backstage user that will be responsible for the model
     */
    owner: string;
    /**
     * Support information for the model / where to open issues
     */
    support?: string;
    /**
     * Descriptive tags for the model
     */
    tags?: string[];
    /**
     * Information on how the model was trained
     */
    training?: string;
    /**
     * How to use and interact with the model
     */
    usage?: string;
}
