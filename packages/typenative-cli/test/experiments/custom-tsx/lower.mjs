// SPDX-License-Identifier: Apache-2.0
// Lower only this experiment's intrinsic JSX, retaining TypeScript annotations for ScriptC.
export function lowerTsx(ts, source) {
  const syntax = ts.createSourceFile(
    "app.tsx",
    source,
    ts.ScriptTarget.Latest,
    true,
    ts.ScriptKind.TSX,
  );
  const result = ts.transform(syntax, [
    (context) => {
      const factory = context.factory;
      function visit(node) {
        if (ts.isJsxElement(node) || ts.isJsxSelfClosingElement(node)) {
          const opening = ts.isJsxElement(node) ? node.openingElement : node;
          if (
            !ts.isIdentifier(opening.tagName) ||
            !/^[a-z][a-z0-9]*$/.test(opening.tagName.text)
          )
            throw new Error("Only intrinsic JSX tags are supported.");
          const attributes = opening.attributes.properties.map((attribute) => {
            if (
              !ts.isJsxAttribute(attribute) ||
              !ts.isIdentifier(attribute.name)
            )
              throw new Error(
                "JSX spread and namespaced attributes are unsupported.",
              );
            const init = attribute.initializer;
            if (!init || !ts.isJsxExpression(init) || !init.expression)
              throw new Error("Use expression attributes in this fixture.");
            return factory.createPropertyAssignment(
              attribute.name.text,
              ts.visitNode(init.expression, visit),
            );
          });
          const children = [];
          if (ts.isJsxElement(node)) {
            for (const child of node.children) {
              if (ts.isJsxText(child) && !child.text.trim()) continue;
              if (!ts.isJsxElement(child) && !ts.isJsxSelfClosingElement(child))
                throw new Error("Use the text property for text bindings.");
              children.push(ts.visitNode(child, visit));
            }
          }
          return factory.createCallExpression(
            factory.createIdentifier("h"),
            undefined,
            [
              factory.createStringLiteral(opening.tagName.text),
              attributes.length
                ? factory.createObjectLiteralExpression(attributes)
                : factory.createNull(),
              ...children,
            ],
          );
        }
        return ts.visitEachChild(node, visit, context);
      }
      return (node) => ts.visitNode(node, visit);
    },
  ]);
  try {
    return ts.createPrinter().printFile(result.transformed[0]);
  } finally {
    result.dispose();
  }
}
