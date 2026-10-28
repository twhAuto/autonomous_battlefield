// Names a battle controller for people: Scripted, or the model with its reasoning effort.
export interface ControllerIdentity { type: string; profileId?: string; modelIdentifier?: string; reasoningEffort?: string }

export function controllerName(controller: ControllerIdentity) {
  if (controller.type !== 'model') return 'Scripted'
  const model = controller.modelIdentifier || controller.profileId || 'Model'
  return controller.reasoningEffort ? `${model} · ${controller.reasoningEffort}` : model
}
