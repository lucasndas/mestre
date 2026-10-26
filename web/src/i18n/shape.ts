/** The shape of a catalog: the same keys as pt-BR, every leaf a string. */
export type Widen<T> = T extends string ? string : { [K in keyof T]: Widen<T[K]> };
