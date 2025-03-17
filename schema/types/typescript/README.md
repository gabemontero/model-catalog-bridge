# model-catalog

This Typescript type definition is automatically generated from the [model catalog schema](../model-catalog-schema.json) using [quicktype](https://github.com/glideapps/quicktype).

## Updating 

If you are making changes to the schema and need to re-generate the types, run `yarn generate`. 

## Type Generation

The command that `yarn generate` runs to generate the types is 
```
sed 's|#/$defs/modelServerAPI|#/$defs/modelServer/$defs/modelServerAPI|g' model-catalog.schema.json | quicktype -s schema -o model-catalog.d.ts --just-types
```

**Note:** Due to implementation differences between different JSON Schema parsers, some parsers treat references against nested names as relative references, and others absolute. Quicktype requires absolute references, which is why we must use `sed` to change our reference to `modelServerAPI` from relative to absolute.