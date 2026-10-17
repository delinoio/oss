import { createElement, isValidElement, cloneElement, Children, createRef } from "react";
const element = createElement("button", { title: "native" }, "hello");
const cloned = cloneElement(element, { title: "updated" });
if (cloned === element || cloned.type !== "button" || cloned.props.children !== "hello" || element.type !== "button" || element.props.children !== "hello" || element.props.title !== "native" || !isValidElement(cloned) || cloned.props.title !== "updated" || Children.count([element, cloned]) !== 2 || createRef().current !== null) {
  throw new Error("React core probe failed");
}
console.log("typenative-react-core-probe-v1");
